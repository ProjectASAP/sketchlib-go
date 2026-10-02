package asapv1_test

import (
	"fmt"
	"math/bits"
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
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexCanonical)
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
	return asapv1.Marshal(s.Kind, md, p)
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
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexCanonical)
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
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
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

func p12Registers() []byte {
	r := make([]byte, 4096)
	r[0], r[1], r[100], r[4095] = 1, 7, 42, 3
	return r
}

func TestGoldenFixtures(t *testing.T) {
	csCounts := []int64{0, 127, 128, 65536, -1, -33, -32768, -2147483648}
	matrix := func(kind asapv1.KindID, cols uint32, counterType, mode string, ints []int64, floats []float64) *matrixState {
		return &matrixState{Kind: kind, Rows: 2, Cols: cols, CounterType: counterType, Mode: mode, Ints: ints, Floats: floats}
	}
	t.Run("hll_classic_p12", func(t *testing.T) {
		asapv1test.CheckGolden(t, "hll_classic_p12", &hllState{Kind: asapv1.KindHLLClassic, Registers: p12Registers()}, nil)
	})
	t.Run("hll_ertl_mle_p12", func(t *testing.T) {
		asapv1test.CheckGolden(t, "hll_ertl_mle_p12", &hllState{Kind: asapv1.KindHLLErtlMLE, Registers: p12Registers()}, nil)
	})
	t.Run("hll_hip_p12", func(t *testing.T) {
		asapv1test.CheckGolden(t, "hll_hip_p12",
			&hllState{Kind: asapv1.KindHLLHIP, Registers: p12Registers(), HIP: [3]float64{1.5, 2.5, 3.0}}, nil)
	})
	for name, known := range map[string]*matrixState{
		"cms_i64_regular_2x3": matrix(asapv1.KindCountMin, 3, "i64", "regular", []int64{0, 1, 127, 128, 300, 65536}, nil),
		"cms_f64_fast_2x3":    matrix(asapv1.KindCountMin, 3, "f64", "fast", nil, []float64{0, 1.5, 2.25, 3.75, 4.125, 5.0625}),
		"cs_i64_regular_2x4":  matrix(asapv1.KindCountSketch, 4, "i64", "regular", csCounts, nil),
		"cs_i64_fast_2x4":     matrix(asapv1.KindCountSketch, 4, "i64", "fast", csCounts, nil),
		"cs_i32_regular_2x4":  matrix(asapv1.KindCountSketch, 4, "i32", "regular", csCounts, nil),
	} {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, nil) })
	}
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
