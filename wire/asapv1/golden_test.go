package asapv1_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

// The types below hold raw fixture state and follow the codec convention, so
// the fixtures check the envelope, metadata and msgpack helpers end to end.

type matrixState struct {
	Kind        asapv1.KindID
	Rows, Cols  uint32
	CounterType string
	Mode        string
	Ints        []int64
}

func (s *matrixState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", uint64(s.Rows))
	md.Uint("cols", uint64(s.Cols))
	md.Str("counter_type", s.CounterType)
	md.Str("mode", s.Mode)
	p := asapv1.NewEncoder()
	p.Array(1)
	asapv1.EncodeInts(p, s.Ints)
	return asapv1.Marshal(s.Kind, md, p)
}

func (s *matrixState) UnmarshalASAPv1(b []byte) error {
	kind, metadata, payload, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	md, err := asapv1.ReadMetadata(metadata)
	if err != nil {
		return err
	}
	out := matrixState{Kind: kind}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	out.Rows = md.Uint32("rows")
	out.Cols = md.Uint32("cols")
	out.CounterType = md.Str("counter_type")
	out.Mode = md.Str("mode")
	if err := md.Finish(); err != nil {
		return err
	}
	p := asapv1.NewDecoder(payload)
	p.ExpectArray(1)
	if out.CounterType == "i32" {
		for _, v := range asapv1.DecodeInts[int32](p) {
			out.Ints = append(out.Ints, int64(v))
		}
	} else {
		out.Ints = asapv1.DecodeInts[int64](p)
	}
	if err := p.Finish(); err != nil {
		return err
	}
	if uint64(len(out.Ints)) != uint64(out.Rows)*uint64(out.Cols) {
		return fmt.Errorf("%d counts for %dx%d", len(out.Ints), out.Rows, out.Cols)
	}
	*s = out
	return nil
}

// kllState covers the KLL fixtures no Go sketch codec reads: compact KLL and
// i64 items.
type kllState struct {
	Kind     asapv1.KindID
	K, M     uint32
	ItemType string
	Seed     *uint64
	Levels   []uint32
	Ints     []int64
	Floats   []float64
	Coin     [3]uint64
}

func (s *kllState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	md.Uint("k", uint64(s.K))
	md.Uint("m", uint64(s.M))
	md.Str("item_type", s.ItemType)
	if s.Seed != nil {
		md.Uint("seed", *s.Seed)
	}
	p := asapv1.NewEncoder()
	p.Array(3)
	asapv1.EncodeUints(p, s.Levels)
	if s.ItemType == "f64" {
		asapv1.EncodeFloat64s(p, s.Floats)
	} else {
		asapv1.EncodeInts(p, s.Ints)
	}
	asapv1.EncodeUints(p, s.Coin[:])
	return asapv1.Marshal(s.Kind, md, p)
}

func (s *kllState) UnmarshalASAPv1(b []byte) error {
	kind, metadata, payload, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	if kind != asapv1.KindKLL && kind != asapv1.KindKLLDynamic {
		return fmt.Errorf("kind_id %v is not KLL", kind)
	}
	md, err := asapv1.ReadMetadata(metadata)
	if err != nil {
		return err
	}
	out := kllState{Kind: kind}
	md.ExpectVersion(1)
	out.K = md.Uint32("k")
	out.M = md.Uint32("m")
	out.ItemType = md.Str("item_type")
	if md.Has("seed") {
		seed := md.Uint64("seed")
		out.Seed = &seed
	}
	if err := md.Finish(); err != nil {
		return err
	}
	p := asapv1.NewDecoder(payload)
	p.ExpectArray(3)
	out.Levels = asapv1.DecodeUints[uint32](p)
	if out.ItemType == "f64" {
		out.Floats = asapv1.DecodeFloat64s(p)
	} else {
		out.Ints = asapv1.DecodeInts[int64](p)
	}
	p.ExpectArray(3)
	out.Coin = [3]uint64{p.Uint(), p.Uint(), uint64(p.Uint32())}
	if err := p.Finish(); err != nil {
		return err
	}
	*s = out
	return nil
}

type ddState struct {
	Counts         []uint64
	Offset         int64
	Sum, Min, Max  float64
	Signed         bool
	NegativeCounts []uint64
	NegativeOffset int64
	ZeroCount      uint64
	Alpha          float64
}

func (s *ddState) MarshalASAPv1() ([]byte, error) {
	version, fields := uint8(1), 5
	if s.Signed {
		version, fields = 2, 8
	}
	md := asapv1.NewMetadataWriter(version)
	md.Float64("alpha", s.Alpha)
	p := asapv1.NewEncoder()
	p.Array(fields)
	asapv1.EncodeUints(p, s.Counts)
	p.Int(s.Offset)
	p.Float64(s.Sum)
	p.Float64(s.Min)
	p.Float64(s.Max)
	if s.Signed {
		asapv1.EncodeUints(p, s.NegativeCounts)
		p.Int(s.NegativeOffset)
		p.Uint(s.ZeroCount)
	}
	return asapv1.Marshal(asapv1.KindDDSketch, md, p)
}

func (s *ddState) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindDDSketch)
	if err != nil {
		return err
	}
	md.ExpectVersion(1, 2)
	out := ddState{Signed: md.Version() == 2, Alpha: md.Float64("alpha")}
	if err := md.Finish(); err != nil {
		return err
	}
	if out.Signed {
		p.ExpectArray(8)
	} else {
		p.ExpectArray(5)
	}
	out.Counts = asapv1.DecodeUints[uint64](p)
	out.Offset = p.Int()
	out.Sum, out.Min, out.Max = p.Float64(), p.Float64(), p.Float64()
	if out.Signed {
		out.NegativeCounts = asapv1.DecodeUints[uint64](p)
		out.NegativeOffset = p.Int()
		out.ZeroCount = p.Uint()
	}
	if err := p.Finish(); err != nil {
		return err
	}
	*s = out
	return nil
}

func compactKLL(itemType string) *kllState {
	seed := uint64(42)
	s := &kllState{Kind: asapv1.KindKLL, K: 200, M: 8, ItemType: itemType, Seed: &seed,
		Levels: []uint32{0, 50}, Coin: [3]uint64{42, 0, 0}}
	for i := range 50 {
		if itemType == "f64" {
			s.Floats = append(s.Floats, float64(i+1))
		} else {
			s.Ints = append(s.Ints, int64(i+1))
		}
	}
	return s
}

func TestGoldenFixtures(t *testing.T) {
	csCounts := []int64{0, 127, 128, 65536, -1, -33, -32768, -2147483648}
	matrix := func(counterType, mode string) *matrixState {
		return &matrixState{Kind: asapv1.KindCountSketch, Rows: 2, Cols: 4, CounterType: counterType, Mode: mode, Ints: csCounts}
	}
	for name, known := range map[string]*matrixState{
		"cs_i64_regular_2x4": matrix("i64", "regular"),
		"cs_i64_fast_2x4":    matrix("i64", "fast"),
		"cs_i32_regular_2x4": matrix("i32", "regular"),
	} {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, nil) })
	}
	t.Run("kll_f64_k200", func(t *testing.T) { asapv1test.CheckGolden(t, "kll_f64_k200", compactKLL("f64"), nil) })
	t.Run("kll_i64_k200", func(t *testing.T) { asapv1test.CheckGolden(t, "kll_i64_k200", compactKLL("i64"), nil) })
	t.Run("kll_dynamic_i64_k200", func(t *testing.T) {
		ints := []int64{0, 1, -1, 127, -32, 128, -33, 255, -128, 256, -129, 65535, -32768, 65536, -32769,
			4294967295, -2147483648, 4294967296, -2147483649, math.MaxInt64, math.MinInt64}
		asapv1test.CheckGolden(t, "kll_dynamic_i64_k200", &kllState{Kind: asapv1.KindKLLDynamic, K: 200, M: 8,
			ItemType: "i64", Levels: []uint32{0, uint32(len(ints))}, Ints: ints, Coin: [3]uint64{42, 0, 0}}, nil)
	})
	t.Run("ddsketch_positive_a001", func(t *testing.T) {
		asapv1test.CheckGolden(t, "ddsketch_positive_a001", &ddState{Alpha: 0.01,
			Counts: []uint64{1, 0, 127, 128, 300, 65536, 4294967296}, Offset: -40,
			Sum: 2181071000.0, Min: 0.453125, Max: 0.5078125}, nil)
	})
	t.Run("ddsketch_signed_a001", func(t *testing.T) {
		asapv1test.CheckGolden(t, "ddsketch_signed_a001", &ddState{Alpha: 0.01, Signed: true,
			Counts: []uint64{3, 0, 2}, Offset: 310, Sum: 2523.90625, Min: -0.016, Max: 515.0,
			NegativeCounts: []uint64{5, 1}, NegativeOffset: -208, ZeroCount: 7}, nil)
	})
}

func TestEveryFixtureIsWellFormed(t *testing.T) {
	names := asapv1test.Names(t)
	if len(names) == 0 {
		t.Fatal("no fixtures")
	}
	for _, name := range names {
		kind, metadata, payload, err := asapv1.Split(asapv1test.Golden(t, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if kind.Name() == "" {
			t.Errorf("%s: kind_id %v is not in the registry", name, kind)
		}
		md, err := asapv1.ReadMetadata(metadata)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if md.Version() == 0 {
			t.Errorf("%s: metadata_version 0", name)
		}
		p := asapv1.NewDecoder(payload)
		if p.Array(); p.Err() != nil {
			t.Errorf("%s: payload is not an array: %v", name, p.Err())
		}
		p = asapv1.NewDecoder(payload)
		p.Skip()
		if err := p.Finish(); err != nil {
			t.Errorf("%s: payload is not one msgpack value: %v", name, err)
		}
	}
}
