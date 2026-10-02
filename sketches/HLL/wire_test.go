package hll

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

// p12Sketch runs the HLL codec at precision 12, the precision of the fixtures.
type p12Sketch struct {
	Kind      asapv1.KindID
	Registers []uint8
	HIP       [3]float64
}

func (s *p12Sketch) MarshalASAPv1() ([]byte, error) {
	var hip *[3]float64
	if s.Kind == asapv1.KindHLLHIP {
		hip = &s.HIP
	}
	return marshalRegisters(s.Kind, 12, s.Registers, hip)
}

func (s *p12Sketch) UnmarshalASAPv1(b []byte) error {
	kind, _, _, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	regs, hip, err := unmarshalRegisters(b, kind, 12)
	if err != nil {
		return err
	}
	*s = p12Sketch{Kind: kind, Registers: regs, HIP: hip}
	return nil
}

func fixtureRegisters(n int) []uint8 {
	r := make([]uint8, n)
	r[0], r[1], r[100], r[n-1] = 1, 7, 42, 3
	return r
}

func TestASAPv1Golden(t *testing.T) {
	regs := fixtureRegisters(1 << 12)
	for name, known := range map[string]*p12Sketch{
		"hll_classic_p12":  {Kind: asapv1.KindHLLClassic, Registers: regs},
		"hll_ertl_mle_p12": {Kind: asapv1.KindHLLErtlMLE, Registers: regs},
		"hll_hip_p12":      {Kind: asapv1.KindHLLHIP, Registers: regs, HIP: [3]float64{1.5, 2.5, 3.0}},
	} {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, nil) })
	}
}

// p14Bytes is the p12 fixture name with precision 14 and regs as its registers.
func p14Bytes(t *testing.T, name string, regs []uint8, hip []float64) []byte {
	t.Helper()
	kind, metadata, _, err := asapv1.Split(asapv1test.Golden(t, name))
	if err != nil {
		t.Fatal(err)
	}
	p12 := []byte("\xa9precision\x0c")
	if !bytes.HasSuffix(metadata, p12) {
		t.Fatalf("%s: metadata does not end with precision 12", name)
	}
	metadata = append(bytes.TrimSuffix(metadata, p12), "\xa9precision\x0e"...)
	p := asapv1.NewEncoder()
	p.Array(1 + len(hip))
	p.Bin(regs)
	for _, v := range hip {
		p.Float64(v)
	}
	b, err := asapv1.Encode(kind, metadata, p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func marshal(t *testing.T, m asapv1.Marshaler) []byte {
	t.Helper()
	b, err := m.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestASAPv1SketchTypes(t *testing.T) {
	regs := fixtureRegisters(HLLRegisterCount)

	h := NewHyperLogLog()
	copy(h.Registers.AsMutSlice(), regs)
	b := marshal(t, h)
	asapv1test.Equal(t, b, p14Bytes(t, "hll_ertl_mle_p12", regs, nil))
	var gotH HyperLogLog
	if err := gotH.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&gotH, h) {
		t.Fatal("HyperLogLog round trip differs")
	}

	for variant, fixture := range map[HLLVariant]string{HLLRegular: "hll_classic_p12", HLLDataFusion: "hll_ertl_mle_p12"} {
		v := NewHyperLogLogVariant(variant)
		copy(v.Registers.AsMutSlice(), regs)
		b := marshal(t, v)
		asapv1test.Equal(t, b, p14Bytes(t, fixture, regs, nil))
		var got HyperLogLogVariant
		if err := got.UnmarshalASAPv1(b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(&got, v) {
			t.Fatalf("HyperLogLogVariant %d round trip differs", variant)
		}
	}

	hip := &HyperLogLogHIP{Registers: storage.Vector1DFromSlice(regs), kxq0: 1.5, kxq1: 2.5, est: 3.0}
	b = marshal(t, hip)
	asapv1test.Equal(t, b, p14Bytes(t, "hll_hip_p12", regs, []float64{1.5, 2.5, 3.0}))
	var gotHIP HyperLogLogHIP
	if err := gotHIP.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&gotHIP, hip) {
		t.Fatal("HyperLogLogHIP round trip differs")
	}
}

func TestASAPv1RoundTripPreservesEstimates(t *testing.T) {
	h, regular, hip := NewHyperLogLog(), NewRegular(), NewHIP()
	for i := 0; i < 20_000; i++ {
		hash := common.Hash64([]byte(fmt.Sprintf("k:%d", i)))
		h.InsertWithHash(hash)
		regular.InsertWithHash(hash)
		hip.InsertWithHash(hash)
	}
	var gotH HyperLogLog
	var gotRegular HyperLogLogVariant
	var gotHIP HyperLogLogHIP
	for _, c := range []struct {
		src asapv1.Marshaler
		dst asapv1.Unmarshaler
	}{{h, &gotH}, {regular, &gotRegular}, {hip, &gotHIP}} {
		if err := c.dst.UnmarshalASAPv1(marshal(t, c.src)); err != nil {
			t.Fatal(err)
		}
	}
	if gotH.Estimate() != h.Estimate() || gotRegular.Estimate() != regular.Estimate() || gotHIP.Estimate() != hip.Estimate() {
		t.Fatal("estimates differ after a round trip")
	}
	gotHIP.InsertWithHash(common.Hash64([]byte("next")))
	hip.InsertWithHash(common.Hash64([]byte("next")))
	if !reflect.DeepEqual(&gotHIP, hip) {
		t.Fatal("HIP state diverges after a round trip")
	}
}

func TestASAPv1RejectsOtherKindsAndPrecisions(t *testing.T) {
	regs := make([]uint8, HLLRegisterCount)
	classic := marshal(t, &HyperLogLogVariant{Registers: storage.Vector1DFromSlice(regs), Variant: HLLRegular})
	ertl := marshal(t, NewHyperLogLog())
	hip := marshal(t, NewHIP())
	relabeled := bytes.Replace(ertl, []byte("\xa9precision\x0e"), []byte("\xa9precision\x0c"), 1)
	cases := []struct {
		name string
		dst  asapv1.Unmarshaler
		b    []byte
	}{
		{"classic into HyperLogLog", &HyperLogLog{}, classic},
		{"hip into HyperLogLog", &HyperLogLog{}, hip},
		{"hip into HyperLogLogVariant", &HyperLogLogVariant{}, hip},
		{"ertl into HyperLogLogHIP", &HyperLogLogHIP{}, ertl},
		{"p12 into HyperLogLog", &HyperLogLog{}, asapv1test.Golden(t, "hll_ertl_mle_p12")},
		{"p12 into HyperLogLogVariant", &HyperLogLogVariant{}, asapv1test.Golden(t, "hll_classic_p12")},
		{"p12 into HyperLogLogHIP", &HyperLogLogHIP{}, asapv1test.Golden(t, "hll_hip_p12")},
		{"p14 registers labeled p12", &HyperLogLog{}, relabeled},
		{"count-min into HyperLogLogVariant", &HyperLogLogVariant{}, asapv1test.Golden(t, "cms_i64_regular_2x3")},
	}
	for _, c := range cases {
		if err := c.dst.UnmarshalASAPv1(c.b); err == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
}

func TestASAPv1RegisterRange(t *testing.T) {
	h := NewHyperLogLog()
	h.Registers.AsMutSlice()[7] = 65 - HLLPrecision
	b := marshal(t, h)

	b[len(b)-HLLRegisterCount+7] = 66 - HLLPrecision
	before := NewHyperLogLog()
	got := NewHyperLogLog()
	if err := got.UnmarshalASAPv1(b); err == nil {
		t.Fatal("decoded a register above the maximum rank")
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatal("a failed decode changed the sketch")
	}

	h.Registers.AsMutSlice()[7] = 66 - HLLPrecision
	if _, err := h.MarshalASAPv1(); err == nil {
		t.Fatal("encoded a register above the maximum rank")
	}
	hip := NewHIP()
	hip.Registers.AsMutSlice()[0] = 255
	if _, err := hip.MarshalASAPv1(); err == nil {
		t.Fatal("encoded a HIP register above the maximum rank")
	}
}

func TestASAPv1UnencodableStates(t *testing.T) {
	sampled := NewHyperLogLog().WithSampleP(0.5)
	if _, err := sampled.MarshalASAPv1(); err == nil {
		t.Error("encoded a sampled sketch")
	}
	short := &HyperLogLogVariant{Registers: storage.Vector1DFromSlice(make([]uint8, 100)), Variant: HLLRegular}
	if _, err := short.MarshalASAPv1(); err == nil {
		t.Error("encoded 100 registers")
	}
	unknown := NewHyperLogLogVariant(HLLVariant(7))
	if _, err := unknown.MarshalASAPv1(); err == nil {
		t.Error("encoded an unknown variant")
	}
	for _, m := range []asapv1.Marshaler{&HyperLogLog{}, &HyperLogLogVariant{}, &HyperLogLogHIP{}} {
		if _, err := m.MarshalASAPv1(); err == nil {
			t.Errorf("%T: encoded a sketch with no registers", m)
		}
	}
}
