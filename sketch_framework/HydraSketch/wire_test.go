package hydrasketch

import (
	"bytes"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

var goldenSchema = []string{"region", "service"}

func mustSchema(t *testing.T, labels []string) keySchema {
	t.Helper()
	ks, err := newKeySchema(labels)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// cellState is a counter's state as its own codec encodes it.
func cellState(t *testing.T, c HydraCounter) []byte {
	t.Helper()
	var b []byte
	var err error
	switch c := c.(type) {
	case *countMinCounter:
		b, err = c.s.MarshalASAPv1()
	case *countSketchCounter:
		b, err = c.s.MarshalASAPv1()
	case *hllCounter:
		b, err = c.s.MarshalASAPv1()
	case *kllCounter:
		e := asapv1.NewEncoder()
		e.Uint(uint64(c.s.K()))
		e.Uint(uint64(c.s.M()))
		err = c.s.EncodeASAPv1Payload(e)
		b = e.Bytes()
	case *univMonCounter[uint64]:
		b, err = c.s.MarshalASAPv1()
	case *univMonCounter[string]:
		b, err = c.s.MarshalASAPv1()
	default:
		t.Fatalf("no state for %T", c)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// protoState is cellState without a KLL prototype's time-seeded coin.
func protoState(t *testing.T, c HydraCounter) []byte {
	t.Helper()
	if c, ok := c.(*kllCounter); ok {
		empty, err := kllEmpty(c.s)
		if err != nil {
			t.Fatal(err)
		}
		e := asapv1.NewEncoder()
		e.Uint(uint64(c.s.K()))
		e.Uint(uint64(c.s.M()))
		e.Bool(empty)
		return e.Bytes()
	}
	return cellState(t, c)
}

func sameHydra(t *testing.T) func(got, want *Hydra) bool {
	return func(got, want *Hydra) bool {
		if got.rows != want.rows || got.cols != want.cols || !slices.Equal(got.schema.labels, want.schema.labels) ||
			!slices.Equal(got.schema.escaped, want.schema.escaped) || got.maskBits != want.maskBits || got.mask != want.mask ||
			reflect.TypeOf(got.proto) != reflect.TypeOf(want.proto) || len(got.cells) != len(want.cells) ||
			!bytes.Equal(protoState(t, got.proto), protoState(t, want.proto)) {
			return false
		}
		for i := range got.cells {
			if !bytes.Equal(cellState(t, got.cells[i]), cellState(t, want.cells[i])) {
				return false
			}
		}
		return true
	}
}

func goldenMatrix(t *testing.T, cs bool, cells [4][4]float64) *Hydra {
	t.Helper()
	newCounter := NewHydraCountMinCounter
	if cs {
		newCounter = NewHydraCountSketchCounter
	}
	proto, err := newCounter(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	counters := make([]HydraCounter, 4)
	for i, vals := range cells {
		c, _ := newCounter(2, 2)
		for j, v := range vals {
			switch c := c.(type) {
			case *countMinCounter:
				c.s.SetCell(j/2, j%2, v)
			case *countSketchCounter:
				c.s.SetCell(j/2, j%2, v)
			}
		}
		counters[i] = c
	}
	return newGrid(2, 2, mustSchema(t, goldenSchema), counters, proto)
}

func goldenKLL(t *testing.T) *Hydra {
	t.Helper()
	values := [4][]float64{{1, 2, 3, 4, 5}, nil, {2.5, -1.0, 0.0, 1e300, -0.125}, {3.0e-5}}
	cells := make([]HydraCounter, 4)
	for i, vs := range values {
		s := kll.InitWithSeed(200, 8, int64(i+1))
		for _, v := range vs {
			s.Update(v)
		}
		cells[i] = &kllCounter{s: s}
	}
	return newGrid(2, 2, mustSchema(t, goldenSchema), cells, NewHydraKLLCounter(200, 8))
}

func goldenHLL(t *testing.T) *Hydra {
	t.Helper()
	sets := []map[int]uint8{{0: 1, 1: 7, 100: 42, 16383: 3}, {0: 2, 8192: 51}}
	cells := make([]HydraCounter, 2)
	for i, set := range sets {
		c := NewHydraHLLCounter().(*hllCounter)
		regs := c.s.Registers.AsMutSlice()
		for idx, v := range set {
			regs[idx] = v
		}
		cells[i] = c
	}
	return newGrid(1, 2, mustSchema(t, goldenSchema), cells, NewHydraHLLCounter())
}

// univMonCell0 is the hydra_univmon_1x2 fixture's first cell, decoded by
// UnivMon from a payload written field by field.
func univMonCell0(t *testing.T) *univmon.UnivMon[uint64] {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("layer_size", 2)
	md.Uint("sketch_row", 1)
	md.Uint("sketch_col", 2)
	md.Uint("heap_size", 2)
	md.Str("key_type", "u64")
	p := asapv1.NewEncoder()
	p.Array(8)
	asapv1.EncodeInts(p, []int64{5, -3, 0, 2})
	asapv1.EncodeInts(p, []int64{34, 4})
	asapv1.EncodeUints(p, []uint32{2, 1})
	asapv1.EncodeUints(p, []uint64{7, 300, 4294967296})
	asapv1.EncodeInts(p, []int64{5, 2, 2})
	p.Array(2)
	p.Bool(false)
	p.Bool(true)
	p.Uint(7)
	p.Uint(uint64(univmon.UpdateModeStandard))
	b, err := asapv1.Marshal(asapv1.KindUnivMon, md, p)
	if err != nil {
		t.Fatal(err)
	}
	u := new(univmon.UnivMon[uint64])
	if err := u.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	return u
}

func goldenUnivMon(t *testing.T) *Hydra {
	t.Helper()
	proto, err := NewHydraUnivMonCounter[uint64](2, 1, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := proto.Clone()
	cells := []HydraCounter{&univMonCounter[uint64]{s: univMonCell0(t)}, empty}
	return newGrid(1, 2, mustSchema(t, goldenSchema), cells, proto)
}

func TestASAPv1Goldens(t *testing.T) {
	asapv1test.CheckGolden(t, "hydra_cm_2x2_counter_2x2", goldenMatrix(t, false, [4][4]float64{
		{0, 1, 127, 128}, {255, 256, 300, 65535}, {65536, 1000000, 2147483647, 0}, {},
	}), sameHydra(t))
	asapv1test.CheckGolden(t, "hydra_cs_2x2_counter_2x2", goldenMatrix(t, true, [4][4]float64{
		{0, -1, 127, -32}, {-33, 128, -128, -129}, {-32768, 65536, -32769, 2147483647}, {-2147483648, 1, 0, 0},
	}), sameHydra(t))
	asapv1test.CheckGolden(t, "hydra_hll_1x2_p14", goldenHLL(t), sameHydra(t))
	asapv1test.CheckGolden(t, "hydra_kll_2x2_k200", goldenKLL(t), sameHydra(t))
	asapv1test.CheckGolden(t, "hydra_univmon_1x2", goldenUnivMon(t), sameHydra(t))
}

func TestASAPv1KindsRouteToTheirVariant(t *testing.T) {
	want := map[string]HydraCounterType{
		"hydra_cm_2x2_counter_2x2": HydraCounterCM,
		"hydra_cs_2x2_counter_2x2": HydraCounterCS,
		"hydra_hll_1x2_p14":        HydraCounterHLL,
		"hydra_kll_2x2_k200":       HydraCounterKLL,
		"hydra_univmon_1x2":        HydraCounterUniversal,
	}
	for name, typ := range want {
		var h Hydra
		if err := h.UnmarshalASAPv1(asapv1test.Golden(t, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.CounterType() != typ || !slices.Equal(h.Schema(), goldenSchema) {
			t.Errorf("%s: decoded a %s grid over %q", name, h.CounterType(), h.Schema())
		}
	}
	var h Hydra
	if err := h.UnmarshalASAPv1(asapv1test.Golden(t, "cms_i64_regular_2x3")); err == nil {
		t.Error("a Count-Min envelope decoded as Hydra")
	}
}

// reframe returns b with its kind_id replaced.
func reframe(t *testing.T, b []byte, kind asapv1.KindID) []byte {
	t.Helper()
	_, md, p, err := asapv1.Split(b)
	if err != nil {
		t.Fatal(err)
	}
	out, err := asapv1.Encode(kind, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestASAPv1VariantsRejectEachOthersEnvelopes(t *testing.T) {
	names := map[asapv1.KindID]string{
		asapv1.KindHydraCountMin:    "hydra_cm_2x2_counter_2x2",
		asapv1.KindHydraCountSketch: "hydra_cs_2x2_counter_2x2",
		asapv1.KindHydraHLL:         "hydra_hll_1x2_p14",
		asapv1.KindHydraKLL:         "hydra_kll_2x2_k200",
		asapv1.KindHydraUnivMon:     "hydra_univmon_1x2",
	}
	for kind, name := range names {
		for other := range names {
			if other == kind || (kind == asapv1.KindHydraCountMin && other == asapv1.KindHydraCountSketch) ||
				(kind == asapv1.KindHydraCountSketch && other == asapv1.KindHydraCountMin) {
				continue
			}
			var h Hydra
			if err := h.UnmarshalASAPv1(reframe(t, asapv1test.Golden(t, name), other)); err == nil {
				t.Errorf("%s decoded as kind %s", name, other)
			}
		}
	}
}

// metadataWith re-encodes golden's metadata with edit applied to its keys.
func metadataWith(t *testing.T, name string, edit func(md *asapv1.MetadataWriter, key string, value []byte) bool) []byte {
	t.Helper()
	kind, mdBytes, payload, err := asapv1.Split(asapv1test.Golden(t, name))
	if err != nil {
		t.Fatal(err)
	}
	d := asapv1.NewDecoder(mdBytes)
	n := d.Map()
	md := asapv1.NewMetadataWriter(1)
	for range n {
		key := d.Str()
		value := d.Raw()
		if key == "metadata_version" {
			continue
		}
		if !edit(md, key, value) {
			md.Field(key).Raw(value)
		}
	}
	e := asapv1.NewEncoder()
	e.Raw(payload)
	b, err := asapv1.Marshal(kind, md, e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestASAPv1RejectsCraftedMetadata(t *testing.T) {
	cases := map[string]struct {
		fixture string
		edit    func(md *asapv1.MetadataWriter, key string, value []byte) bool
	}{
		"zero rows": {"hydra_cm_2x2_counter_2x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "rows" {
				md.Uint(key, 0)
			}
			return key == "rows"
		}},
		"21 rows": {"hydra_hll_1x2_p14", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "rows" {
				md.Uint(key, 21)
			}
			return key == "rows"
		}},
		"larger grid than payload": {"hydra_kll_2x2_k200", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "cols" {
				md.Uint(key, 3)
			}
			return key == "cols"
		}},
		"21 counter rows": {"hydra_cs_2x2_counter_2x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_rows" {
				md.Uint(key, 21)
			}
			return key == "counter_rows"
		}},
		"huge counter cols": {"hydra_cm_2x2_counter_2x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_cols" {
				md.Uint(key, math.MaxUint32)
			}
			return key == "counter_cols"
		}},
		"i64 counters": {"hydra_cm_2x2_counter_2x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_type" {
				md.Str(key, "i64")
			}
			return key == "counter_type"
		}},
		"regular mode": {"hydra_cs_2x2_counter_2x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_mode" {
				md.Str(key, "regular")
			}
			return key == "counter_mode"
		}},
		"precision 12": {"hydra_hll_1x2_p14", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_precision" {
				md.Uint(key, 12)
			}
			return key == "counter_precision"
		}},
		"k below m": {"hydra_kll_2x2_k200", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_k" {
				md.Uint(key, 4)
			}
			return key == "counter_k"
		}},
		"i64 items": {"hydra_kll_2x2_k200", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_item_type" {
				md.Str(key, "i64")
			}
			return key == "counter_item_type"
		}},
		"zero heap": {"hydra_univmon_1x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_heap_size" {
				md.Uint(key, 0)
			}
			return key == "counter_heap_size"
		}},
		"string keys over u64 cells": {"hydra_univmon_1x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_key_type" {
				md.Str(key, "string")
			}
			return key == "counter_key_type"
		}},
		"usize keys": {"hydra_univmon_1x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_key_type" {
				md.Str(key, "usize")
			}
			return key == "counter_key_type"
		}},
		"duplicate schema labels": {"hydra_hll_1x2_p14", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "schema" {
				md.Strs(key, []string{"a", "a"})
			}
			return key == "schema"
		}},
		"empty schema": {"hydra_hll_1x2_p14", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "schema" {
				md.Strs(key, nil)
			}
			return key == "schema"
		}},
		"missing schema": {"hydra_kll_2x2_k200", func(_ *asapv1.MetadataWriter, key string, _ []byte) bool {
			return key == "schema"
		}},
		"unknown key": {"hydra_univmon_1x2", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "counter_key_type" {
				md.Str(key, "u64")
				md.Uint("extra", 1)
				return true
			}
			return false
		}},
		"seed index on KLL": {"hydra_kll_2x2_k200", func(md *asapv1.MetadataWriter, key string, _ []byte) bool {
			if key == "rows" {
				md.Uint("canonical_seed_index", 5)
			}
			return false
		}},
	}
	for name, tc := range cases {
		var h Hydra
		if err := h.UnmarshalASAPv1(metadataWith(t, tc.fixture, tc.edit)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	var h Hydra
	if err := h.UnmarshalASAPv1(metadataWith(t, "hydra_kll_2x2_k200", func(*asapv1.MetadataWriter, string, []byte) bool {
		return false
	})); err != nil {
		t.Fatalf("unedited metadata: %v", err)
	}
}

func TestASAPv1RejectsUnencodableGrids(t *testing.T) {
	mixed := goldenMatrix(t, false, [4][4]float64{})
	mixed.cells[3], _ = NewHydraCountSketchCounter(2, 2)
	wide := goldenMatrix(t, false, [4][4]float64{})
	wide.cells[1], _ = NewHydraCountMinCounter(2, 4)
	fractional := goldenMatrix(t, false, [4][4]float64{{0.5}})
	overflow := goldenMatrix(t, false, [4][4]float64{{math.MaxInt32 + 1}})
	dirtyProto := goldenMatrix(t, false, [4][4]float64{})
	dirtyProto.proto.(*countMinCounter).s.SetCell(0, 0, 1)
	kllK := goldenKLL(t)
	kllK.cells[2] = NewHydraKLLCounter(100, 8)
	dirtyKLL := goldenKLL(t)
	dirtyKLL.proto.(*kllCounter).s.Update(1)
	dirtyHLL := goldenHLL(t)
	dirtyHLL.proto.(*hllCounter).s.Registers.AsMutSlice()[3] = 1
	shape := goldenUnivMon(t)
	shape.cells[1], _ = NewHydraUnivMonCounter[uint64](3, 1, 2, 2)
	keys := goldenUnivMon(t)
	keys.cells[1], _ = NewHydraUnivMonCounter[string](2, 1, 2, 2)
	dirtyUniv := goldenUnivMon(t)
	if err := dirtyUniv.proto.Insert(common.FromU64(1), 1); err != nil {
		t.Fatal(err)
	}
	tall, _ := NewHydra(21, 2, goldenSchema, NewHydraHLLCounter())
	for name, h := range map[string]*Hydra{
		"mixed variants": mixed, "cell geometry": wide, "fractional count": fractional, "i32 overflow": overflow,
		"count-min prototype with data": dirtyProto, "KLL k": kllK, "KLL prototype with data": dirtyKLL,
		"HLL prototype with data": dirtyHLL, "UnivMon shape": shape, "UnivMon key types": keys,
		"UnivMon prototype with data": dirtyUniv, "21 rows": tall,
	} {
		if _, err := h.MarshalASAPv1(); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
}

func TestASAPv1RejectsCraftedPayloads(t *testing.T) {
	short := func(name string, cut int) []byte {
		kind, md, p, err := asapv1.Split(asapv1test.Golden(t, name))
		if err != nil {
			t.Fatal(err)
		}
		d := asapv1.NewDecoder(p)
		d.ExpectArray(1)
		e := asapv1.NewEncoder()
		e.Array(1)
		switch kind {
		case asapv1.KindHydraHLL:
			regs := d.Bin()
			e.Bin(regs[:len(regs)-cut])
		case asapv1.KindHydraCountMin, asapv1.KindHydraCountSketch:
			counts := asapv1.DecodeInts[int64](d)
			asapv1.EncodeInts(e, counts[:len(counts)-cut])
		default:
			n := d.Array()
			e.Array(n - cut)
			for range n - cut {
				e.Raw(d.Raw())
			}
		}
		b, err := asapv1.Encode(kind, md, e.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, name := range []string{"hydra_cm_2x2_counter_2x2", "hydra_cs_2x2_counter_2x2", "hydra_hll_1x2_p14", "hydra_kll_2x2_k200", "hydra_univmon_1x2"} {
		var h Hydra
		if err := h.UnmarshalASAPv1(short(name, 1)); err == nil {
			t.Errorf("%s: a payload one cell short decoded", name)
		}
		if err := h.UnmarshalASAPv1(append(asapv1test.Golden(t, name), 0xc0)); err != nil {
			t.Errorf("%s: trailing bytes after the envelope rejected: %v", name, err)
		}
	}
	hllMax := goldenHLL(t)
	hllMax.cells[0].(*hllCounter).s.Registers.AsMutSlice()[5] = 52
	if _, err := hllMax.MarshalASAPv1(); err == nil || !strings.Contains(err.Error(), "rank") {
		t.Errorf("an HLL register above 51 encoded: %v", err)
	}
}

func TestASAPv1RoundTripKeepsQueries(t *testing.T) {
	type probe struct {
		key []*string
		q   HydraQuery
	}
	build := func(counter HydraCounter, value func(i int) *common.SketchInput) *Hydra {
		h, err := NewHydra(3, 16, []string{"src", "dst"}, counter)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 60 {
			key := []string{[]string{"a", "b", "c"}[i%3], []string{"x", "y"}[i%2]}
			if err := h.Update(key, value(i), int64(1+i%4)); err != nil {
				t.Fatal(err)
			}
		}
		return h
	}
	cm, _ := NewHydraCountMinCounter(2, 32)
	cs, _ := NewHydraCountSketchCounter(2, 32)
	um, _ := NewHydraUnivMonCounter[string](4, 2, 16, 3)
	keyed := func(i int) *common.SketchInput { return common.FromString([]string{"p", "q", "r", "s"}[i%4]) }
	cases := []struct {
		h      *Hydra
		probes []probe
	}{
		{build(cm, keyed), []probe{{[]*string{Eq("a"), nil}, FrequencyQuery(common.FromString("p"))}, {[]*string{nil, Eq("y")}, FrequencyQuery(common.FromString("q"))}}},
		{build(cs, keyed), []probe{{[]*string{Eq("b"), Eq("x")}, FrequencyQuery(common.FromString("r"))}}},
		{build(NewHydraHLLCounter(), func(i int) *common.SketchInput { return common.FromU64(uint64(i)) }), []probe{{[]*string{Eq("c"), nil}, CardinalityQuery()}}},
		{build(NewHydraKLLCounter(200, 8), func(i int) *common.SketchInput { return common.FromF64(float64(i)) }), []probe{{[]*string{nil, Eq("x")}, QuantileQuery(0.5)}, {[]*string{Eq("a"), nil}, CDFQuery(0.3)}}},
		{build(um, keyed), []probe{{[]*string{Eq("a"), nil}, L1NormQuery()}, {[]*string{nil, Eq("x")}, EntropyQuery()}, {[]*string{Eq("c"), Eq("y")}, CardinalityQuery()}}},
	}
	for _, tc := range cases {
		b, err := tc.h.MarshalASAPv1()
		if err != nil {
			t.Fatalf("%s: %v", tc.h.CounterType(), err)
		}
		var got Hydra
		if err := got.UnmarshalASAPv1(b); err != nil {
			t.Fatalf("%s: %v", tc.h.CounterType(), err)
		}
		again, err := got.MarshalASAPv1()
		if err != nil || !bytes.Equal(again, b) {
			t.Fatalf("%s: re-encode differs (%v)", tc.h.CounterType(), err)
		}
		for _, p := range tc.probes {
			want, err1 := tc.h.QueryKey(p.key, p.q)
			have, err2 := got.QueryKey(p.key, p.q)
			if err1 != nil || err2 != nil || want != have {
				t.Errorf("%s: query %+v gives %v (%v) after a round trip, %v (%v) before", tc.h.CounterType(), p.q.Kind, have, err2, want, err1)
			}
		}
		if err := got.Merge(tc.h); err != nil {
			t.Errorf("%s: a decoded grid does not merge with its source: %v", tc.h.CounterType(), err)
		}
	}
}
