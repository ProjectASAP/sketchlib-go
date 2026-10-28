package hll

import (
	"bytes"
	"fmt"
	"math"
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
	HIPEst    float64
}

func (s *p12Sketch) MarshalASAPv1() ([]byte, error) {
	var est *float64
	if s.Kind == asapv1.KindHLLHIP {
		est = &s.HIPEst
	}
	return marshalRegisters(s.Kind, 12, s.Registers, est)
}

func (s *p12Sketch) UnmarshalASAPv1(b []byte) error {
	kind, _, _, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	regs, est, err := unmarshalRegisters(b, kind, 12)
	if err != nil {
		return err
	}
	*s = p12Sketch{Kind: kind, Registers: regs, HIPEst: est}
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
}

// withoutKxq rewrites retired 01 03 HIP bytes into the 01 04 bytes of the same
// state: kind_id 01 04, payload array of 2 instead of 4, and the two float64
// fields after the register bin (9 bytes each) removed.
func withoutKxq(t *testing.T, retired []byte) []byte {
	t.Helper()
	kind, metadata, payload, err := asapv1.Split(retired)
	if err != nil || kind != asapv1.KindHLLHIPWithKxq {
		t.Fatalf("not a retired HIP envelope: %v %v", kind, err)
	}
	if payload[0] != 0x94 {
		t.Fatalf("payload header %#x, want a fixarray of 4", payload[0])
	}
	est := payload[len(payload)-9:]
	regs := payload[1 : len(payload)-27]
	want := append(append([]byte{0x92}, regs...), est...)
	b, err := asapv1.Encode(asapv1.KindHLLHIP, metadata, want)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The retired 01 03 fixtures decode: the registers and estimate are kept,
// kxq0 and kxq1 (1.5 / 2.5 and 16380.5 / 0.25, which no register state
// produces) are replaced by the sums over the registers, and the sketch
// re-encodes as 01 04 with those two fields gone.
func TestASAPv1RetiredHIPDecodes(t *testing.T) {
	var p12 p12Sketch
	if err := p12.UnmarshalASAPv1(asapv1test.Golden(t, "hll_hip_p12")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p12.Registers, fixtureRegisters(1<<12)) || p12.HIPEst != 3.0 {
		t.Fatalf("p12: registers or estimate differ, est %v", p12.HIPEst)
	}

	retired := asapv1test.Golden(t, "hll_hip_p14")
	var got HyperLogLogHIP
	if err := got.UnmarshalASAPv1(retired); err != nil {
		t.Fatal(err)
	}
	regs := make([]uint8, HLLRegisterCount)
	regs[0], regs[1], regs[8192], regs[16383] = 1, 7, 42, 51
	want := HyperLogLogHIP{
		Registers: storage.Vector1DFromSlice(regs),
		kxq0:      16380 + 0.5 + math.Ldexp(1, -7),
		kxq1:      math.Ldexp(1, -42) + math.Ldexp(1, -51),
		est:       4.125,
	}
	if !reflect.DeepEqual(&got, &want) {
		t.Fatalf("decoded %v %v %v, want %v %v %v", got.kxq0, got.kxq1, got.est, want.kxq0, want.kxq1, want.est)
	}
	b := marshal(t, &got)
	if !bytes.Equal(b, withoutKxq(t, retired)) {
		t.Fatal("retired p14 does not re-encode as the same state under 01 04")
	}
	var again HyperLogLogHIP
	if err := again.UnmarshalASAPv1(b); err != nil || !reflect.DeepEqual(&again, &want) {
		t.Fatalf("01 04 bytes do not decode back: %v", err)
	}
}

// Decoding recomputes kxq0 and kxq1 bit for bit, so a decoded sketch that
// keeps inserting holds the same estimate as one that never left memory. Every
// 7th hash is placed at a rank of 32 or more so kxq1 is exercised too.
func TestASAPv1HIPDecodeThenContinueIsExact(t *testing.T) {
	stream := func(n, offset int) []uint64 {
		hs := make([]uint64, n)
		for i := range hs {
			h := common.Hash64([]byte(fmt.Sprintf("c:%d", offset+i)))
			if i%7 == 0 {
				rank := 32 + uint(h>>40)%(65-HLLPrecision-31)
				bucket := h >> HLLRegisterBits
				h = bucket << HLLRegisterBits
				if rank <= HLLRegisterBits {
					h |= 1 << (HLLRegisterBits - rank)
				}
			}
			hs[i] = h
		}
		return hs
	}
	for _, before := range []int{0, 1_000, 40_000} {
		kept := NewHIP()
		for _, h := range stream(before, 0) {
			kept.InsertWithHash(h)
		}
		var restored HyperLogLogHIP
		if err := restored.UnmarshalASAPv1(marshal(t, kept)); err != nil {
			t.Fatal(err)
		}
		for _, h := range stream(40_000, before) {
			kept.InsertWithHash(h)
			restored.InsertWithHash(h)
		}
		if kept.kxq1 == 0 {
			t.Fatal("the stream never reached a rank of 32")
		}
		if !reflect.DeepEqual(&restored, kept) {
			t.Fatalf("after %d updates: est %v vs %v", before, restored.est, kept.est)
		}
		kxq0, kxq1 := hipKxq(kept.Registers.AsSlice())
		if math.Float64bits(kxq0) != math.Float64bits(kept.kxq0) || math.Float64bits(kxq1) != math.Float64bits(kept.kxq1) {
			t.Fatal("incremental kxq0/kxq1 differ from the sums over the registers")
		}
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
