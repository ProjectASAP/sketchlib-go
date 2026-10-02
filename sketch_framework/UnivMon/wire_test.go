package univmon

import (
	"bytes"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

// goldenState is the state of the univmon_*_l3_2x4_h5 fixtures, with keys in
// the order alpha, beta, delta, gamma, epsilon, zeta.
func goldenState[K asapv1.HeapKey](t *testing.T, keys [6]K) *UnivMon[K] {
	t.Helper()
	u, err := NewUnivMon[K](5, 2, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	layers := []struct {
		counts   []int64
		l2       []int64
		entries  []asapv1.HeapEntry[K]
		complete bool
	}{
		{
			[]int64{0, 127, 128, 65536, -1, -33, -32768, -2147483648},
			[]int64{4294999809, 4611686019501130818},
			[]asapv1.HeapEntry[K]{{Key: keys[0], Count: 65536}, {Key: keys[1], Count: 300}, {Key: keys[2], Count: 128}},
			true,
		},
		{
			[]int64{3, -2, 0, 1, 0, 0, 5, -4},
			[]int64{14, 41},
			[]asapv1.HeapEntry[K]{{Key: keys[3], Count: 5}},
			false,
		},
		{
			[]int64{0, 7, 0, 0, -6, 0, 0, 0},
			[]int64{49, 36},
			[]asapv1.HeapEntry[K]{{Key: keys[4], Count: 9}, {Key: keys[5], Count: 2}},
			true,
		},
	}
	for i, l := range layers {
		copy(u.layers[i].counts, l.counts)
		copy(u.layers[i].l2, l.l2)
		for _, e := range l.entries {
			u.heaps[i].update(e.Key, keyID(e.Key), e.Count)
		}
		u.candidateComplete[i] = l.complete
	}
	u.bucketSize = 70000
	u.updateMode = UpdateModeStandard
	return u
}

// sameState compares two pyramids by shape, cells, accumulators, heap
// capacities and entries, candidate flags, weight and mode.
func sameState[K asapv1.HeapKey](got, want *UnivMon[K]) bool {
	if got.heapSize != want.heapSize || got.sketchRow != want.sketchRow || got.sketchCol != want.sketchCol ||
		got.layerSize != want.layerSize || got.bucketSize != want.bucketSize || got.updateMode != want.updateMode ||
		!slices.Equal(got.candidateComplete, want.candidateComplete) ||
		len(got.layers) != len(want.layers) || len(got.heaps) != len(want.heaps) {
		return false
	}
	for i := range got.layers {
		g, w := got.layers[i], want.layers[i]
		if g.rows != w.rows || g.cols != w.cols || g.seedIdx != w.seedIdx || g.maskBits != w.maskBits ||
			!slices.Equal(g.counts, w.counts) || !slices.Equal(g.l2, w.l2) {
			return false
		}
		if got.heaps[i].k != want.heaps[i].k || !reflect.DeepEqual(got.HeapEntries(i), want.HeapEntries(i)) {
			return false
		}
	}
	return true
}

func TestASAPv1Goldens(t *testing.T) {
	asapv1test.CheckGolden(t, "univmon_str_l3_2x4_h5",
		goldenState(t, [6]string{"alpha", "beta", "delta", "gamma", "epsilon", "zeta"}), sameState[string])
	asapv1test.CheckGolden(t, "univmon_i64_l3_2x4_h5",
		goldenState(t, [6]int64{math.MinInt64, -1, -129, 128, 4294967296, 7}), sameState[int64])
	emptyStr, _ := NewUnivMon[string](5, 2, 4, 3)
	asapv1test.CheckGolden(t, "univmon_empty_l3_2x4_h5", emptyStr, sameState[string])
	emptyI64, _ := NewUnivMon[int64](5, 2, 4, 3)
	asapv1test.CheckGolden(t, "univmon_empty_l3_2x4_h5", emptyI64, sameState[int64])
}

func TestASAPv1KeyTypeMustMatch(t *testing.T) {
	var s UnivMon[string]
	if err := s.UnmarshalASAPv1(asapv1test.Golden(t, "univmon_i64_l3_2x4_h5")); err == nil {
		t.Error("i64 keys decoded into a string-keyed pyramid")
	}
	var i UnivMon[int64]
	if err := i.UnmarshalASAPv1(asapv1test.Golden(t, "univmon_str_l3_2x4_h5")); err == nil {
		t.Error("string keys decoded into an i64-keyed pyramid")
	}
	var u UnivMon[uint64]
	if err := u.UnmarshalASAPv1(asapv1test.Golden(t, "cms_i64_regular_2x3")); err == nil {
		t.Error("a Count-Min envelope decoded as UnivMon")
	}
}

func TestASAPv1RoundTripKeepsQueries(t *testing.T) {
	u, _ := NewUnivMon[uint64](8, 2, 16, 4)
	for key := range uint64(40) {
		if err := u.Insert(key, int64(1+key%5)); err != nil {
			t.Fatal(err)
		}
	}
	loads := make([]int, u.layerSize)
	for i, h := range u.heaps {
		loads[i] = len(h.entries)
	}
	if slices.Min(loads) == slices.Max(loads) {
		t.Fatalf("want layers of different heap loads, got %v", loads)
	}
	b, err := u.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var got UnivMon[uint64]
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !sameState(&got, u) {
		t.Fatal("decoded state differs")
	}
	if got.CalcL1() != u.CalcL1() || got.CalcL2() != u.CalcL2() || got.CalcCard() != u.CalcCard() ||
		got.CalcEntropy() != u.CalcEntropy() {
		t.Error("queries differ after a round trip")
	}
	again, err := got.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, again, b)
}

func TestASAPv1CarriesModeAndCandidateFlags(t *testing.T) {
	u, _ := NewUnivMon[uint64](2, 2, 8, 3)
	for key := range uint64(20) {
		if err := u.FastInsert(key, 3); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Contains(u.candidateComplete, false) {
		t.Fatal("want a layer that evicted")
	}
	b, err := u.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var got UnivMon[uint64]
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if got.Mode() != UpdateModeTerminal || !slices.Equal(got.CandidatesComplete(), u.CandidatesComplete()) {
		t.Errorf("mode %d flags %v, want %d %v", got.Mode(), got.CandidatesComplete(), UpdateModeTerminal, u.CandidatesComplete())
	}
	if got.CalcCard() != u.CalcCard() || got.CalcEntropy() != u.CalcEntropy() {
		t.Error("terminal queries differ after a round trip")
	}
}

// rawPyramid is a UnivMon envelope written field by field.
type rawPyramid struct {
	layerSize, sketchRow, sketchCol, heapSize uint64
	keyType                                   string
	extraKey                                  bool
	counts, l2                                []int64
	heapLens                                  []uint64
	keys                                      []string
	heapCounts                                []int64
	complete                                  []bool
	bucketSize, mode                          uint64
}

func (r rawPyramid) encode(t *testing.T) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("layer_size", r.layerSize)
	md.Uint("sketch_row", r.sketchRow)
	md.Uint("sketch_col", r.sketchCol)
	md.Uint("heap_size", r.heapSize)
	md.Str("key_type", r.keyType)
	if r.extraKey {
		md.Uint("seed_index", 0)
	}
	p := asapv1.NewEncoder()
	p.Array(8)
	asapv1.EncodeInts(p, r.counts)
	asapv1.EncodeInts(p, r.l2)
	asapv1.EncodeUints(p, r.heapLens)
	asapv1.EncodeStrs(p, r.keys)
	asapv1.EncodeInts(p, r.heapCounts)
	p.Array(len(r.complete))
	for _, c := range r.complete {
		p.Bool(c)
	}
	p.Uint(r.bucketSize)
	p.Uint(r.mode)
	b, err := asapv1.Marshal(asapv1.KindUnivMon, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// validRaw is two layers of 2x4 with heap 2: "a" and "b" in layer 0, "a" in
// layer 1.
func validRaw() rawPyramid {
	return rawPyramid{
		layerSize: 2, sketchRow: 2, sketchCol: 4, heapSize: 2, keyType: "string",
		counts:     make([]int64, 16),
		l2:         []int64{1, 2, 3, 4},
		heapLens:   []uint64{2, 1},
		keys:       []string{"a", "b", "a"},
		heapCounts: []int64{5, 3, 2},
		complete:   []bool{true, false},
		bucketSize: 10, mode: 1,
	}
}

func TestASAPv1UnmarshalRejects(t *testing.T) {
	var ok UnivMon[string]
	if err := ok.UnmarshalASAPv1(validRaw().encode(t)); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}
	cases := map[string]func(r *rawPyramid){
		"zero layer_size":      func(r *rawPyramid) { r.layerSize = 0 },
		"zero sketch_row":      func(r *rawPyramid) { r.sketchRow = 0 },
		"zero sketch_col":      func(r *rawPyramid) { r.sketchCol = 0 },
		"zero heap_size":       func(r *rawPyramid) { r.heapSize = 0 },
		"65 layers":            func(r *rawPyramid) { r.layerSize = 65 },
		"unknown key_type":     func(r *rawPyramid) { r.keyType = "str" },
		"unknown metadata key": func(r *rawPyramid) { r.extraKey = true },
		"21 rows": func(r *rawPyramid) {
			r.sketchRow = 21
			r.counts = make([]int64, 2*21*4)
			r.l2 = make([]int64, 2*21)
		},
		"geometry past 128 hash bits": func(r *rawPyramid) {
			r.sketchRow, r.sketchCol = 20, 64
			r.counts = make([]int64, 2*20*64)
			r.l2 = make([]int64, 2*20)
		},
		"l2 short of rows*layers":   func(r *rawPyramid) { r.l2 = r.l2[:3] },
		"counts short of cells":     func(r *rawPyramid) { r.counts = r.counts[:15] },
		"heap_lens short of layers": func(r *rawPyramid) { r.heapLens = []uint64{3} },
		"flags short of layers":     func(r *rawPyramid) { r.complete = r.complete[:1] },
		"heap_lens past keys":       func(r *rawPyramid) { r.heapLens = []uint64{2, 2} },
		"keys past heap_counts":     func(r *rawPyramid) { r.heapCounts = r.heapCounts[:2] },
		"layer past heap_size": func(r *rawPyramid) {
			r.heapLens = []uint64{3, 0}
			r.keys = []string{"a", "b", "c"}
		},
		"key twice in a layer":       func(r *rawPyramid) { r.keys = []string{"a", "a", "b"} },
		"negative l2":                func(r *rawPyramid) { r.l2[3] = -1 },
		"update_mode 3":              func(r *rawPyramid) { r.mode = 3 },
		"layer_size past the l2 run": func(r *rawPyramid) { r.layerSize = 3 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRaw()
			mutate(&r)
			u := goldenState(t, [6]string{"alpha", "beta", "delta", "gamma", "epsilon", "zeta"})
			before, _ := u.MarshalASAPv1()
			if err := u.UnmarshalASAPv1(r.encode(t)); err == nil {
				t.Fatal("decoded")
			}
			after, _ := u.MarshalASAPv1()
			if !bytes.Equal(before, after) {
				t.Error("a failed decode changed the receiver")
			}
		})
	}
	t.Run("trailing payload bytes", func(t *testing.T) {
		b := validRaw().encode(t)
		kind, md, payload, _ := asapv1.Split(b)
		b, _ = asapv1.Encode(kind, md, append(slices.Clone(payload), 0xc0))
		var u UnivMon[string]
		if err := u.UnmarshalASAPv1(b); err == nil {
			t.Fatal("decoded")
		}
	})
}

func TestASAPv1MarshalRejects(t *testing.T) {
	cases := map[string]func(u *UnivMon[string]){
		"negative l2":       func(u *UnivMon[string]) { u.layers[1].l2[0] = -1 },
		"layer seed index":  func(u *UnivMon[string]) { u.layers[2].seedIdx = 0 },
		"layer geometry":    func(u *UnivMon[string]) { u.layers[0], _ = newCountL2HH(2, 8, 0) },
		"heap capacity":     func(u *UnivMon[string]) { u.heaps[0].k = 6 },
		"update mode":       func(u *UnivMon[string]) { u.updateMode = 3 },
		"missing flag":      func(u *UnivMon[string]) { u.candidateComplete = u.candidateComplete[:2] },
		"too many layers":   func(u *UnivMon[string]) { u.layerSize = 65 },
		"zero heap size":    func(u *UnivMon[string]) { u.heapSize = 0 },
		"layer count drift": func(u *UnivMon[string]) { u.layerSize = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := goldenState(t, [6]string{"alpha", "beta", "delta", "gamma", "epsilon", "zeta"})
			mutate(u)
			if _, err := u.MarshalASAPv1(); err == nil {
				t.Fatal("encoded")
			}
		})
	}
}

// TestASAPv1NestedPayload checks the hooks against the envelope: the payload
// they write is the envelope's, and they read it back from metadata built
// the way a parent builds it.
func TestASAPv1NestedPayload(t *testing.T) {
	u := goldenState(t, [6]string{"alpha", "beta", "delta", "gamma", "epsilon", "zeta"})
	e := asapv1.NewEncoder()
	if err := u.EncodeASAPv1Payload(e); err != nil {
		t.Fatal(err)
	}
	b, _ := u.MarshalASAPv1()
	_, mdBytes, payload, err := asapv1.Split(b)
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, e.Bytes(), payload)

	w := asapv1.NewMetadataWriter(1)
	w.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	w.Uint("layer_size", 3)
	w.Uint("sketch_row", 2)
	w.Uint("sketch_col", 4)
	w.Uint("heap_size", 5)
	w.Str("key_type", "string")
	asapv1test.Equal(t, w.Bytes(), mdBytes)
	md, err := asapv1.ReadMetadata(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	d := asapv1.NewDecoder(append(slices.Clone(payload), 0xc0))
	var got UnivMon[string]
	if err := got.DecodeASAPv1Payload(md, d); err != nil {
		t.Fatal(err)
	}
	if !sameState(&got, u) {
		t.Error("decoded state differs")
	}
	if !d.Nil() || d.Finish() != nil {
		t.Error("the hook read past its payload")
	}

	w = asapv1.NewMetadataWriter(1)
	w.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	w.Uint("layer_size", 3)
	md, _ = asapv1.ReadMetadata(w.Bytes())
	err = new(UnivMon[string]).DecodeASAPv1Payload(md, asapv1.NewDecoder(payload))
	if err == nil || !strings.Contains(err.Error(), "sketch_row") {
		t.Errorf("metadata missing keys: got %v", err)
	}
}
