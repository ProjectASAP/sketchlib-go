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

func (s *kllState) writeMetadata(md *asapv1.MetadataWriter) {
	md.Uint("k", uint64(s.K))
	md.Uint("m", uint64(s.M))
	md.Str("item_type", s.ItemType)
	if s.Seed != nil {
		md.Uint("seed", *s.Seed)
	}
}

func (s *kllState) EncodeASAPv1Payload(p *asapv1.Encoder) error {
	p.Array(3)
	asapv1.EncodeUints(p, s.Levels)
	if s.ItemType == "f64" {
		asapv1.EncodeFloat64s(p, s.Floats)
	} else {
		asapv1.EncodeInts(p, s.Ints)
	}
	asapv1.EncodeUints(p, s.Coin[:])
	return p.Err()
}

func (s *kllState) DecodeASAPv1Payload(md *asapv1.MetadataReader, p *asapv1.Decoder) error {
	out := kllState{Kind: s.Kind}
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
	p.ExpectArray(3)
	out.Levels = asapv1.DecodeUints[uint32](p)
	if out.ItemType == "f64" {
		out.Floats = asapv1.DecodeFloat64s(p)
	} else {
		out.Ints = asapv1.DecodeInts[int64](p)
	}
	p.ExpectArray(3)
	out.Coin = [3]uint64{p.Uint(), p.Uint(), uint64(p.Uint32())}
	if err := p.Err(); err != nil {
		return err
	}
	*s = out
	return nil
}

func (s *kllState) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	s.writeMetadata(md)
	p := asapv1.NewEncoder()
	if err := s.EncodeASAPv1Payload(p); err != nil {
		return nil, err
	}
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
	p := asapv1.NewDecoder(payload)
	if err := out.DecodeASAPv1Payload(md, p); err != nil {
		return err
	}
	if err := p.Finish(); err != nil {
		return err
	}
	*s = out
	return nil
}

// kllGrid carries KLL payloads as the elements of its own payload, with the
// cells' k, m and item_type held once in its metadata.
type kllGrid struct {
	K, M     uint32
	ItemType string
	Cells    []kllState
}

func (g *kllGrid) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("counter_k", uint64(g.K))
	md.Uint("counter_m", uint64(g.M))
	md.Str("counter_item_type", g.ItemType)
	p := asapv1.NewEncoder()
	p.Array(1)
	p.Array(len(g.Cells))
	for i := range g.Cells {
		if err := g.Cells[i].EncodeASAPv1Payload(p); err != nil {
			return nil, err
		}
	}
	return asapv1.Marshal(asapv1.KindHydraKLL, md, p)
}

func (g *kllGrid) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindHydraKLL)
	if err != nil {
		return err
	}
	out := kllGrid{}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	out.K = md.Uint32("counter_k")
	out.M = md.Uint32("counter_m")
	out.ItemType = md.Str("counter_item_type")
	if err := md.Finish(); err != nil {
		return err
	}
	cell := asapv1.NewMetadataWriter(1)
	(&kllState{K: out.K, M: out.M, ItemType: out.ItemType}).writeMetadata(cell)
	p.ExpectArray(1)
	out.Cells = make([]kllState, p.Array())
	for i := range out.Cells {
		cellMD, err := asapv1.ReadMetadata(cell.Bytes())
		if err != nil {
			return err
		}
		out.Cells[i].Kind = asapv1.KindKLL
		if err := out.Cells[i].DecodeASAPv1Payload(cellMD, p); err != nil {
			return fmt.Errorf("cell %d: %w", i, err)
		}
	}
	if err := p.Finish(); err != nil {
		return err
	}
	*g = out
	return nil
}

func p12Registers() []byte {
	r := make([]byte, 4096)
	r[0], r[1], r[100], r[4095] = 1, 7, 42, 3
	return r
}

func kllFixture(itemType string) *kllState {
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
	t.Run("kll_i64_k200", func(t *testing.T) { asapv1test.CheckGolden(t, "kll_i64_k200", kllFixture("i64"), nil) })
	t.Run("kll_f64_k200", func(t *testing.T) { asapv1test.CheckGolden(t, "kll_f64_k200", kllFixture("f64"), nil) })
}

func TestNestedPayloads(t *testing.T) {
	a, b := kllFixture("i64"), kllFixture("i64")
	b.Seed, b.Levels, b.Ints, b.Coin = nil, []uint32{0, 2}, []int64{-5, 9}, [3]uint64{1, 2, 3}
	a.Seed = nil
	grid := &kllGrid{K: 200, M: 8, ItemType: "i64", Cells: []kllState{*a, *b}}
	bytes, err := grid.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var got kllGrid
	if err := got.UnmarshalASAPv1(bytes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, grid) {
		t.Fatalf("decoded %+v, want %+v", got, grid)
	}

	md, p, err := asapv1.Open(bytes, asapv1.KindHydraKLL)
	if err != nil {
		t.Fatal(err)
	}
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint32("counter_k")
	md.Uint32("counter_m")
	md.Str("counter_item_type")
	if err := md.Finish(); err != nil {
		t.Fatal(err)
	}
	p.ExpectArray(1)
	p.ExpectArray(2)
	first := p.Raw()
	cellPayload := asapv1.NewEncoder()
	if err := a.EncodeASAPv1Payload(cellPayload); err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, first, cellPayload.Bytes())
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
