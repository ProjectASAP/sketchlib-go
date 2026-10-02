package asapv1_test

import (
	"fmt"
	"math/bits"
	"reflect"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

// The types below hold raw fixture state and follow the codec convention, so
// the fixtures check the envelope, metadata and msgpack helpers end to end.

type hllState struct {
	Kind      asapv1.KindID
	Registers []byte
	HIP       [3]float64
}

func (s *hllState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter()
	md.HashSpec(asapv1.StandardProfile(), asapv1.CanonicalSeedIndex)
	md.Uint("precision", uint64(bits.TrailingZeros(uint(len(s.Registers)))))
	p := asapv1.NewEncoder()
	if s.Kind == asapv1.KindHLLHIP {
		p.Array(4)
		p.Bin(s.Registers)
		for _, v := range s.HIP {
			p.Float64(v)
		}
	} else {
		p.Array(1)
		p.Bin(s.Registers)
	}
	return asapv1.Encode(s.Kind, md.Bytes(), p.Bytes())
}

func (s *hllState) UnmarshalASAPv1(b []byte) error {
	kind, metadata, payload, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	if kind != asapv1.KindHLLClassic && kind != asapv1.KindHLLErtlMLE && kind != asapv1.KindHLLHIP {
		return fmt.Errorf("kind_id %v is not HLL", kind)
	}
	md, err := asapv1.ReadMetadata(metadata)
	if err != nil {
		return err
	}
	md.HashSpec(asapv1.StandardProfile(), asapv1.CanonicalSeedIndex)
	precision := md.Uint8("precision")
	if err := md.Finish(); err != nil {
		return err
	}
	out := hllState{Kind: kind}
	p := asapv1.NewDecoder(payload)
	if kind == asapv1.KindHLLHIP {
		p.ExpectArray(4)
		out.Registers = p.Bin()
		for i := range out.HIP {
			out.HIP[i] = p.Float64()
		}
	} else {
		p.ExpectArray(1)
		out.Registers = p.Bin()
	}
	if err := p.Finish(); err != nil {
		return err
	}
	if len(out.Registers) != 1<<precision {
		return fmt.Errorf("%d registers at precision %d", len(out.Registers), precision)
	}
	*s = out
	return nil
}

type matrixState struct {
	Kind        asapv1.KindID
	Rows, Cols  uint32
	CounterType string
	Mode        string
	Ints        []int64
	Floats      []float64
}

func (s *matrixState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter()
	md.HashSpec(asapv1.StandardProfile(), asapv1.MatrixSeedIndex)
	md.Uint("rows", uint64(s.Rows))
	md.Uint("cols", uint64(s.Cols))
	md.Str("counter_type", s.CounterType)
	md.Str("mode", s.Mode)
	p := asapv1.NewEncoder()
	p.Array(1)
	if s.CounterType == "f64" {
		asapv1.EncodeFloat64s(p, s.Floats)
	} else {
		asapv1.EncodeInts(p, s.Ints)
	}
	return asapv1.Encode(s.Kind, md.Bytes(), p.Bytes())
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
	md.HashSpec(asapv1.StandardProfile(), asapv1.MatrixSeedIndex)
	out.Rows = md.Uint32("rows")
	out.Cols = md.Uint32("cols")
	out.CounterType = md.Str("counter_type")
	out.Mode = md.Str("mode")
	if err := md.Finish(); err != nil {
		return err
	}
	p := asapv1.NewDecoder(payload)
	p.ExpectArray(1)
	switch out.CounterType {
	case "f64":
		out.Floats = asapv1.DecodeFloat64s(p)
	case "i32":
		for _, v := range asapv1.DecodeInts[int32](p) {
			out.Ints = append(out.Ints, int64(v))
		}
	default:
		out.Ints = asapv1.DecodeInts[int64](p)
	}
	if err := p.Finish(); err != nil {
		return err
	}
	if n := len(out.Ints) + len(out.Floats); uint64(n) != uint64(out.Rows)*uint64(out.Cols) {
		return fmt.Errorf("%d counts for %dx%d", n, out.Rows, out.Cols)
	}
	*s = out
	return nil
}

type kllState struct {
	K, M     uint32
	ItemType string
	Seed     *uint64
	Levels   []uint32
	Ints     []int64
	Floats   []float64
	Coin     [3]uint64
}

func (s *kllState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter()
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
	return asapv1.Encode(asapv1.KindKLL, md.Bytes(), p.Bytes())
}

func (s *kllState) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindKLL)
	if err != nil {
		return err
	}
	var out kllState
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

func p12Registers() []byte {
	r := make([]byte, 4096)
	r[0], r[1], r[100], r[4095] = 1, 7, 42, 3
	return r
}

func TestGoldenFixtures(t *testing.T) {
	csCounts := []int64{0, 127, 128, 65536, -1, -33, -32768, -2147483648}
	seed := uint64(42)
	kllInts := make([]int64, 50)
	kllFloats := make([]float64, 50)
	for i := range 50 {
		kllInts[i] = int64(i + 1)
		kllFloats[i] = float64(i + 1)
	}
	cases := []struct {
		name  string
		known interface {
			asapv1.Marshaler
			asapv1.Unmarshaler
		}
		fresh interface {
			asapv1.Marshaler
			asapv1.Unmarshaler
		}
	}{
		{"hll_classic_p12", &hllState{Kind: asapv1.KindHLLClassic, Registers: p12Registers()}, &hllState{}},
		{"hll_ertl_mle_p12", &hllState{Kind: asapv1.KindHLLErtlMLE, Registers: p12Registers()}, &hllState{}},
		{"hll_hip_p12", &hllState{Kind: asapv1.KindHLLHIP, Registers: p12Registers(), HIP: [3]float64{1.5, 2.5, 3.0}}, &hllState{}},
		{"cms_i64_regular_2x3", &matrixState{Kind: asapv1.KindCountMin, Rows: 2, Cols: 3, CounterType: "i64", Mode: "regular",
			Ints: []int64{0, 1, 127, 128, 300, 65536}}, &matrixState{}},
		{"cms_f64_fast_2x3", &matrixState{Kind: asapv1.KindCountMin, Rows: 2, Cols: 3, CounterType: "f64", Mode: "fast",
			Floats: []float64{0, 1.5, 2.25, 3.75, 4.125, 5.0625}}, &matrixState{}},
		{"cs_i64_regular_2x4", &matrixState{Kind: asapv1.KindCountSketch, Rows: 2, Cols: 4, CounterType: "i64", Mode: "regular",
			Ints: csCounts}, &matrixState{}},
		{"cs_i64_fast_2x4", &matrixState{Kind: asapv1.KindCountSketch, Rows: 2, Cols: 4, CounterType: "i64", Mode: "fast",
			Ints: csCounts}, &matrixState{}},
		{"cs_i32_regular_2x4", &matrixState{Kind: asapv1.KindCountSketch, Rows: 2, Cols: 4, CounterType: "i32", Mode: "regular",
			Ints: csCounts}, &matrixState{}},
		{"kll_i64_k200", &kllState{K: 200, M: 8, ItemType: "i64", Seed: &seed, Levels: []uint32{0, 50}, Ints: kllInts,
			Coin: [3]uint64{42, 0, 0}}, &kllState{}},
		{"kll_f64_k200", &kllState{K: 200, M: 8, ItemType: "f64", Seed: &seed, Levels: []uint32{0, 50}, Floats: kllFloats,
			Coin: [3]uint64{42, 0, 0}}, &kllState{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asapv1test.CheckMarshal(t, c.name, c.known)
			asapv1test.CheckRoundTrip(t, c.name, c.fresh)
			if !reflect.DeepEqual(c.fresh, c.known) {
				t.Fatalf("decoded %+v, want %+v", c.fresh, c.known)
			}
		})
	}
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
		if _, err := asapv1.ReadMetadata(metadata); err != nil {
			t.Errorf("%s: %v", name, err)
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
