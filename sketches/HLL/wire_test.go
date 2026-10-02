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

// p12Sketch runs the HLL codec at precision 12, the precision of the p12 fixtures.
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

func marshal(t *testing.T, m asapv1.Marshaler) []byte {
	t.Helper()
	b, err := m.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestASAPv1GoldenP14(t *testing.T) {
	regs := func() []uint8 {
		r := make([]uint8, HLLRegisterCount)
		r[0], r[1], r[8192], r[16383] = 1, 7, 42, 51
		return r
	}
	t.Run("hll_ertl_mle_p14/HyperLogLog", func(t *testing.T) {
		h := NewHyperLogLog()
		copy(h.Registers.AsMutSlice(), regs())
		asapv1test.CheckGolden(t, "hll_ertl_mle_p14", h, nil)
	})
	t.Run("hll_classic_p14/HyperLogLogVariant", func(t *testing.T) {
		v := &HyperLogLogVariant{Registers: storage.Vector1DFromSlice(regs()), Variant: HLLRegular}
		asapv1test.CheckGolden(t, "hll_classic_p14", v, nil)
	})
	t.Run("hll_ertl_mle_p14/HyperLogLogVariant", func(t *testing.T) {
		v := &HyperLogLogVariant{Registers: storage.Vector1DFromSlice(regs()), Variant: HLLDataFusion}
		asapv1test.CheckGolden(t, "hll_ertl_mle_p14", v, nil)
	})
	t.Run("hll_hip_p14/HyperLogLogHIP", func(t *testing.T) {
		hip := &HyperLogLogHIP{Registers: storage.Vector1DFromSlice(regs()), kxq0: 16380.5, kxq1: 0.25, est: 4.125}
		asapv1test.CheckGolden(t, "hll_hip_p14", hip, nil)
	})
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
