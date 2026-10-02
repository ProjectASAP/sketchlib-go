package hydrasketch

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/zeebo/xxh3"
)

const epsilon = 1e-6

var k3 = []string{"c0", "c1", "c2"}

func mustHydra(t *testing.T, rows, cols int, schema []string, counter HydraCounter, err error) *Hydra {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHydra(rows, cols, schema, counter)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mustUpdate(t *testing.T, h *Hydra, key []string, value *common.SketchInput, count int64) {
	t.Helper()
	if err := h.Update(key, value, count); err != nil {
		t.Fatal(err)
	}
}

func mustQuery(t *testing.T, h *Hydra, key []*string, q HydraQuery) float64 {
	t.Helper()
	v, err := h.QueryKey(key, q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestKeySchemaEncodingNamesItsColumns(t *testing.T) {
	s3 := mustSchema(t, []string{"a", "b", "c"})
	s2 := mustSchema(t, []string{"x", "y"})
	var b strings.Builder
	cases := []struct {
		s      keySchema
		values []string
		mask   uint32
		want   string
	}{
		{s3, []string{"p", "q", "r"}, 0b001, "a:p"},
		{s3, []string{"p", "q", "r"}, 0b010, "b:q"},
		{s3, []string{"p", "q", "r"}, 0b101, "a:p;c:r"},
		{s2, []string{"p", "r"}, 0b11, "x:p;y:r"},
		{s2, []string{"x;y", "z"}, 0b11, `x:x\;y;y:z`},
		{s2, []string{"x", "y;z"}, 0b11, `x:x;y:y\;z`},
		{s2, []string{"a:b", "c"}, 0b11, `x:a\:b;y:c`},
		{s2, []string{`a\b`, "c"}, 0b11, `x:a\\b;y:c`},
		{mustSchema(t, []string{"a;b", "c:d"}), []string{"p", "q"}, 0b11, `a\;b:p;c\:d:q`},
		{s2, []string{"", "c"}, 0b11, "x:;y:c"},
		{s2, []string{"", "c"}, 0b10, "y:c"},
	}
	for _, tc := range cases {
		if got := tc.s.subkey(&b, tc.values, tc.mask); got != tc.want {
			t.Errorf("subkey(%q, %03b) = %q, want %q", tc.values, tc.mask, got, tc.want)
		}
	}
}

func TestKeySchemaRejectsInvalidColumnLists(t *testing.T) {
	tooMany := make([]string, MaxKeyColumns+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("c%d", i)
	}
	for _, labels := range [][]string{nil, {"a", "a"}, tooMany, {"\xff"}} {
		if _, err := newKeySchema(labels); err == nil {
			t.Errorf("schema %q accepted", labels)
		}
	}
	if _, err := newKeySchema(tooMany[:MaxKeyColumns]); err != nil {
		t.Errorf("%d columns rejected: %v", MaxKeyColumns, err)
	}
}

func TestSubkeysAreLabelledByColumn(t *testing.T) {
	value := common.FromString("pkt")
	small := func() (HydraCounter, error) { return NewHydraCountMinCounter(2, 64) }
	freq := func(h *Hydra, key ...*string) float64 { return mustQuery(t, h, key, FrequencyQuery(value)) }

	c, err := small()
	h := mustHydra(t, 3, 512, []string{"src", "dst"}, c, err)
	for range 10 {
		mustUpdate(t, h, []string{"alice", "bob"}, value, 1)
	}
	for _, tc := range []struct {
		got, want float64
	}{
		{freq(h, Eq("alice"), nil), 10}, {freq(h, nil, Eq("alice")), 0},
		{freq(h, nil, Eq("bob")), 10}, {freq(h, Eq("bob"), nil), 0},
	} {
		if tc.got != tc.want {
			t.Errorf("cross-column frequency %v, want %v", tc.got, tc.want)
		}
	}

	c, err = small()
	h2 := mustHydra(t, 3, 512, []string{"a", "b"}, c, err)
	mustUpdate(t, h2, []string{"x;y", "z"}, value, 1)
	mustUpdate(t, h2, []string{"x", "y;z"}, value, 1)
	for _, got := range []float64{freq(h2, Eq("x;y"), nil), freq(h2, Eq("x"), nil), freq(h2, nil, Eq("y;z")), freq(h2, nil, Eq("z"))} {
		if got != 1 {
			t.Errorf("escaped frequency %v, want 1", got)
		}
	}

	c, err = small()
	h3 := mustHydra(t, 3, 512, k3, c, err)
	mustUpdate(t, h3, []string{"p", "q", "r"}, value, 1)
	mustUpdate(t, h3, []string{"p", "other", "r"}, value, 1)
	if got := freq(h3, Eq("p"), nil, Eq("r")); got != 2 {
		t.Errorf("interior unconstrained column: %v, want 2", got)
	}
	if got := freq(h3, Eq("p"), Eq("q"), Eq("r")); got != 1 {
		t.Errorf("full key: %v, want 1", got)
	}
	if got := freq(h3, nil, Eq("q"), nil); got != 1 {
		t.Errorf("middle column: %v, want 1", got)
	}

	if err := h3.Update([]string{"p", "q"}, value, 1); err == nil {
		t.Error("an update of the wrong arity succeeded")
	}
	if err := h3.Update([]string{"p", "q", "r"}, nil, 1); err == nil {
		t.Error("a nil value updated")
	}
	if _, err := h3.QueryKey([]*string{Eq("p")}, FrequencyQuery(value)); err == nil {
		t.Error("a query of the wrong arity succeeded")
	}
	if _, err := h3.QueryKey([]*string{nil, nil, nil}, FrequencyQuery(value)); err == nil {
		t.Error("a query constraining no column succeeded")
	}
	if _, err := h3.QueryKey([]*string{Eq("p"), nil, nil}, CardinalityQuery()); err == nil {
		t.Error("a cardinality query on Count-Min cells succeeded")
	}
	for _, schema := range [][]string{{"a", "a"}, nil} {
		c, _ := small()
		if _, err := NewHydra(3, 64, schema, c); err == nil {
			t.Errorf("schema %q accepted", schema)
		}
	}
	c, _ = small()
	if _, err := NewHydra(0, 64, k3, c); err == nil {
		t.Error("a grid of zero rows accepted")
	}
}

func TestMergeRejectsSchemaMismatch(t *testing.T) {
	value := common.FromString("pkt")
	grid := func(schema ...string) *Hydra {
		c, err := NewHydraCountMinCounter(2, 64)
		return mustHydra(t, 3, 64, schema, c, err)
	}
	a := grid("src", "dst")
	reordered := grid("dst", "src")
	mustUpdate(t, a, []string{"alice", "bob"}, value, 1)
	mustUpdate(t, reordered, []string{"bob", "alice"}, value, 1)
	for name, o := range map[string]*Hydra{"reordered": reordered, "different": grid("src", "port"), "narrower": grid("src")} {
		if err := a.Merge(o); err == nil {
			t.Errorf("merged a %s schema", name)
		}
	}
	hll := mustHydra(t, 3, 64, []string{"src", "dst"}, NewHydraHLLCounter(), nil)
	if err := a.Merge(hll); err == nil {
		t.Error("merged an HLL grid into a Count-Min grid")
	}
	same := grid("src", "dst")
	mustUpdate(t, same, []string{"alice", "bob"}, value, 1)
	if err := a.Merge(same); err != nil {
		t.Fatal(err)
	}
	if got := mustQuery(t, a, []*string{Eq("alice"), nil}, FrequencyQuery(value)); got != 2 {
		t.Errorf("merged frequency %v, want 2", got)
	}
}

var subpopulationRows = []struct {
	key   []string
	value float64
}{
	{[]string{"key1", "key2", "key3"}, 10}, {[]string{"key1", "key2", "key4"}, 10},
	{[]string{"key1", "key2", "key3"}, 20}, {[]string{"key1", "key2", "key3"}, 30},
	{[]string{"key4", "key5", "key6"}, 40}, {[]string{"key4", "key5", "key6"}, 50}, {[]string{"key4", "key5", "key6"}, 60},
	{[]string{"key7", "key8", "key9"}, 70}, {[]string{"key7", "key8", "key9"}, 80}, {[]string{"key7", "key8", "key9"}, 90},
}

func TestSubpopulationFrequency(t *testing.T) {
	c, err := NewHydraCountMinCounter(3, 4096)
	h := mustHydra(t, 3, 64, k3, c, err)
	for _, r := range subpopulationRows {
		mustUpdate(t, h, r.key, common.FromF64(r.value), 1)
	}
	for _, tc := range []struct {
		key   []*string
		value float64
		want  float64
	}{
		{[]*string{Eq("key1"), nil, nil}, 10, 2}, {[]*string{Eq("key1"), nil, nil}, 20, 1},
		{[]*string{Eq("key1"), nil, nil}, 30, 1}, {[]*string{Eq("key4"), nil, nil}, 40, 1},
		{[]*string{Eq("key1"), nil, Eq("key3")}, 10, 1}, {[]*string{Eq("key1"), Eq("key2"), Eq("key3")}, 20, 1},
		{[]*string{Eq("key1"), Eq("key8"), nil}, 10, 0},
	} {
		if got := mustQuery(t, h, tc.key, FrequencyQuery(common.FromF64(tc.value))); got != tc.want {
			t.Errorf("frequency of %v: %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestSubpopulationCardinality(t *testing.T) {
	h := mustHydra(t, 5, 128, k3, NewHydraHLLCounter(), nil)
	for _, r := range subpopulationRows {
		if r.key[2] == "key4" {
			continue
		}
		mustUpdate(t, h, r.key, common.FromF64(r.value), 1)
	}
	for _, tc := range []struct {
		key  []*string
		want float64
	}{
		{[]*string{Eq("key1"), nil, nil}, 3}, {[]*string{Eq("key4"), nil, nil}, 3}, {[]*string{Eq("key7"), nil, nil}, 3},
		{[]*string{Eq("key1"), Eq("key2"), nil}, 3}, {[]*string{Eq("key1"), Eq("key2"), Eq("key3")}, 3},
		{[]*string{Eq("key1"), Eq("key8"), nil}, 0}, {[]*string{Eq("unknown"), nil, nil}, 0},
	} {
		if got := mustQuery(t, h, tc.key, CardinalityQuery()); math.Abs(got-tc.want) > epsilon {
			t.Errorf("cardinality %v, want %v", got, tc.want)
		}
	}
}

func TestKLLCDFs(t *testing.T) {
	h := mustHydra(t, 3, 1024, k3, NewHydraKLLCounter(200, 8), nil)
	for _, r := range subpopulationRows {
		if r.key[2] == "key4" {
			continue
		}
		mustUpdate(t, h, r.key, common.FromF64(r.value), 1)
	}
	for _, tc := range []struct {
		key       []*string
		threshold float64
		want      float64
	}{
		{[]*string{Eq("key1"), nil, nil}, 15, 1.0 / 3}, {[]*string{Eq("key1"), nil, nil}, 25, 2.0 / 3},
		{[]*string{Eq("key1"), nil, nil}, 35, 1}, {[]*string{Eq("key4"), nil, nil}, 55, 2.0 / 3},
		{[]*string{Eq("key7"), nil, nil}, 75, 1.0 / 3}, {[]*string{Eq("key1"), nil, Eq("key3")}, 25, 2.0 / 3},
		{[]*string{Eq("key4"), Eq("key5"), Eq("key6")}, 65, 1}, {[]*string{Eq("key1"), Eq("key5"), nil}, 50, 0},
		{[]*string{Eq("key1"), nil, nil}, 0, 0}, {[]*string{Eq("key1"), nil, nil}, 100, 1},
		{[]*string{Eq("unknown"), nil, nil}, 50, 0},
	} {
		got, err := h.QueryQuantile(tc.key, tc.threshold)
		if err != nil || math.Abs(got-tc.want) > epsilon {
			t.Errorf("CDF at %v: %v (%v), want %v", tc.threshold, got, err, tc.want)
		}
	}
	if err := h.Update([]string{"a", "b", "c"}, common.FromString("x"), 1); err == nil {
		t.Error("a KLL counter took a string value")
	}
}

func TestCounterQueries(t *testing.T) {
	cm, _ := NewHydraCountMinCounter(3, 4096)
	cs, _ := NewHydraCountSketchCounter(3, 4096)
	for _, c := range []HydraCounter{cm, cs} {
		for range 3 {
			if err := c.Insert(common.FromU64(42), 1); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := c.Query(FrequencyQuery(common.FromU64(42))); err != nil || got != 3 {
			t.Errorf("%s frequency %v (%v), want 3", c.CounterType(), got, err)
		}
		for _, q := range []HydraQuery{QuantileQuery(0.5), CardinalityQuery()} {
			if _, err := c.Query(q); err == nil {
				t.Errorf("%s answered query kind %d", c.CounterType(), q.Kind)
			}
		}
	}
	if err := cm.Merge(cs); err == nil {
		t.Error("merged a Count Sketch counter into a Count-Min counter")
	}

	h := NewHydraHLLCounter()
	for i := range 100 {
		_ = h.Insert(common.FromU64(uint64(i)), 1)
	}
	_ = h.Insert(common.FromU64(0), 1)
	if got, _ := h.Query(CardinalityQuery()); got < 90 || got > 110 {
		t.Errorf("HLL cardinality %v, want about 100", got)
	}

	k := NewHydraKLLCounter(200, 8)
	for i := 1; i <= 100; i++ {
		_ = k.Insert(common.FromF64(float64(i)), 1)
	}
	if got, _ := k.Query(QuantileQuery(0.5)); math.Abs(got-50) >= 5 {
		t.Errorf("KLL median %v, want about 50", got)
	}

	u, _ := NewHydraUnivMonCounter[string](32, 3, 1024, 8)
	_ = u.Insert(common.FromString("A"), 10)
	_ = u.Insert(common.FromString("B"), 20)
	if got, _ := u.Query(L1NormQuery()); got != 30 {
		t.Errorf("UnivMon L1 %v, want 30", got)
	}
	if got, _ := u.Query(CardinalityQuery()); math.Abs(got-2) >= 0.5 {
		t.Errorf("UnivMon cardinality %v, want about 2", got)
	}
	if got, _ := u.Query(EntropyQuery()); got <= 0 {
		t.Errorf("UnivMon entropy %v, want positive", got)
	}
	if _, err := u.Query(FrequencyQuery(common.FromString("A"))); err == nil {
		t.Error("UnivMon answered a frequency query")
	}
}

func TestUnivMonCellKeysHashOnlyValuesByHash(t *testing.T) {
	c, err := NewHydraUnivMonCounter[string](8, 3, 1024, 8)
	if err != nil {
		t.Fatal(err)
	}
	inserted := map[string]bool{}
	for i := range 200 {
		hash := common.Hash64([]byte(strconv.Itoa(i)))
		inserted[strconv.FormatUint(hash, 16)] = true
		if err := c.Insert(&common.SketchInput{Hash: hash}, 1); err != nil {
			t.Fatal(err)
		}
	}
	u := c.(*univMonCounter[string]).s
	if card := u.CalcCard(); card < 150 || card > 250 {
		t.Errorf("cardinality of 200 distinct hash-only values: %v", card)
	}
	if h := u.CalcEntropy(); math.Abs(h-math.Log2(200)) > 1 {
		t.Errorf("entropy of 200 uniform hash-only values: %v, want about %v", h, math.Log2(200))
	}
	top := u.HeapEntries(0)
	if len(top) == 0 {
		t.Fatal("layer 0 heap is empty")
	}
	for _, e := range top {
		if !inserted[e.Key] {
			t.Errorf("heap key %q is not the hex hash of an inserted value", e.Key)
		}
	}
	empty, _ := NewHydraUnivMonCounter[string](8, 3, 1024, 8)
	_ = empty.Insert(common.FromString(""), 1)
	if es := empty.(*univMonCounter[string]).s.HeapEntries(0); len(es) != 1 || es[0].Key != "" {
		t.Errorf("the empty string keyed as %v", es)
	}
}

func TestInputKey(t *testing.T) {
	if k, err := inputKey[uint64](common.FromU64(1 << 40)); err != nil || k != 1<<40 {
		t.Errorf("uint64 key %v (%v)", k, err)
	}
	if k, err := inputKey[int8](common.FromU64(uint64(0xffffffffffffff80))); err != nil || k != -128 {
		t.Errorf("int8 key %v (%v)", k, err)
	}
	if _, err := inputKey[uint8](common.FromU64(256)); err == nil {
		t.Error("256 keyed as a uint8")
	}
	if _, err := inputKey[uint64](common.FromString("abc")); err == nil {
		t.Error("3 bytes keyed as a uint64")
	}
	if k, err := inputKey[float64](common.FromF64(2.5)); err != nil || k != 2.5 {
		t.Errorf("float64 key %v (%v)", k, err)
	}
	if _, err := inputKey[float32](common.FromF64(0.1)); err == nil {
		t.Error("0.1 keyed as a float32")
	}
	if k, err := inputKey[[]byte](common.FromString("ab")); err != nil || string(k) != "ab" {
		t.Errorf("bytes key %q (%v)", k, err)
	}
}

func TestUpdatePlacesSubkeysAtHydraSeed(t *testing.T) {
	c, err := NewHydraCountMinCounter(1, 4)
	h := mustHydra(t, 3, 8, []string{"src"}, c, err)
	value := common.FromString("pkt")
	mustUpdate(t, h, []string{"a;b"}, value, 1)
	hash := xxh3.HashSeed([]byte(`src:a\;b`), common.SeedList()[6])
	for r := range 3 {
		for col := range 8 {
			got, _ := h.Cell(r, col).Query(FrequencyQuery(value))
			want := 0.0
			if uint64(col) == hash>>(3*r)&7 {
				want = 1
			}
			if got != want {
				t.Errorf("cell (%d, %d) holds %v, want %v", r, col, got, want)
			}
		}
	}
}
