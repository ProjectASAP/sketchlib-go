package hll

import (
	"errors"
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// checkRegisters fails unless regs holds 2^precision registers, none above the
// largest rank an insert can write at that precision (65 - precision).
func checkRegisters(regs []uint8, precision uint8) error {
	if len(regs) != 1<<precision {
		return fmt.Errorf("hll: %d registers, want %d at precision %d", len(regs), 1<<precision, precision)
	}
	limit := 65 - precision
	for i, r := range regs {
		if r > limit {
			return fmt.Errorf("hll: register %d holds rank %d, above the maximum %d at precision %d", i, r, limit, precision)
		}
	}
	return nil
}

// marshalRegisters encodes an HLL of the given kind and precision. hip is the
// HIP payload's kxq0, kxq1 and estimate, and is nil for every other kind.
func marshalRegisters(kind asapv1.KindID, precision uint8, regs []uint8, hip *[3]float64) ([]byte, error) {
	if err := checkRegisters(regs, precision); err != nil {
		return nil, err
	}
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexCanonical)
	md.Uint("precision", uint64(precision))
	p := asapv1.NewEncoder()
	if kind == asapv1.KindHLLHIP {
		p.Array(4)
		p.Bin(regs)
		for _, v := range hip {
			p.Float64(v)
		}
	} else {
		p.Array(1)
		p.Bin(regs)
	}
	return asapv1.Marshal(kind, md, p)
}

// unmarshalRegisters decodes an HLL whose kind_id must be kind and whose
// precision must be precision. hip is zero for every kind but HIP.
func unmarshalRegisters(b []byte, kind asapv1.KindID, precision uint8) (regs []uint8, hip [3]float64, err error) {
	md, p, err := asapv1.Open(b, kind)
	if err != nil {
		return nil, hip, err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexCanonical)
	md.ExpectUint("precision", uint64(precision))
	if err := md.Finish(); err != nil {
		return nil, hip, err
	}
	if kind == asapv1.KindHLLHIP {
		p.ExpectArray(4)
		regs = p.Bin()
		for i := range hip {
			hip[i] = p.Float64()
		}
	} else {
		p.ExpectArray(1)
		regs = p.Bin()
	}
	if err := p.Finish(); err != nil {
		return nil, hip, err
	}
	if err := checkRegisters(regs, precision); err != nil {
		return nil, hip, err
	}
	return regs, hip, nil
}

// MarshalASAPv1 encodes the sketch as ASAPv1 HLL Ertl-MLE. A sampled sketch
// (SampleP below 1) has no encoding.
func (h *HyperLogLog) MarshalASAPv1() ([]byte, error) {
	if h.SampleP() < 1 {
		return nil, fmt.Errorf("hll: a sketch sampled at p=%v has no ASAPv1 encoding", h.SampleP())
	}
	if h.sparse == nil && h.Registers == nil {
		return nil, errors.New("hll: sketch has no registers")
	}
	return marshalRegisters(asapv1.KindHLLErtlMLE, HLLPrecision, h.RegisterSlice(), nil)
}

// UnmarshalASAPv1 replaces the sketch with a dense, unsampled one decoded from
// ASAPv1 HLL Ertl-MLE bytes at precision HLLPrecision.
func (h *HyperLogLog) UnmarshalASAPv1(b []byte) error {
	regs, _, err := unmarshalRegisters(b, asapv1.KindHLLErtlMLE, HLLPrecision)
	if err != nil {
		return err
	}
	*h = HyperLogLog{Registers: storage.Vector1DFromSlice(regs), sampleP: 1.0}
	return nil
}

// MarshalASAPv1 encodes the sketch as ASAPv1 HLL Classic (HLLRegular) or
// HLL Ertl-MLE (HLLDataFusion).
func (h *HyperLogLogVariant) MarshalASAPv1() ([]byte, error) {
	var kind asapv1.KindID
	switch h.Variant {
	case HLLRegular:
		kind = asapv1.KindHLLClassic
	case HLLDataFusion:
		kind = asapv1.KindHLLErtlMLE
	default:
		return nil, fmt.Errorf("hll: variant %d has no ASAPv1 encoding", h.Variant)
	}
	if h.Registers == nil {
		return nil, errors.New("hll: sketch has no registers")
	}
	return marshalRegisters(kind, HLLPrecision, h.Registers.AsSlice(), nil)
}

// UnmarshalASAPv1 replaces the sketch with one decoded from ASAPv1 HLL Classic
// or HLL Ertl-MLE bytes at precision HLLPrecision; the kind_id sets Variant.
func (h *HyperLogLogVariant) UnmarshalASAPv1(b []byte) error {
	kind, _, _, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	var variant HLLVariant
	switch kind {
	case asapv1.KindHLLClassic:
		variant = HLLRegular
	case asapv1.KindHLLErtlMLE:
		variant = HLLDataFusion
	default:
		return fmt.Errorf("hll: kind_id %v is not HLL Classic or HLL Ertl-MLE", kind)
	}
	regs, _, err := unmarshalRegisters(b, kind, HLLPrecision)
	if err != nil {
		return err
	}
	*h = HyperLogLogVariant{Registers: storage.Vector1DFromSlice(regs), Variant: variant}
	return nil
}

// MarshalASAPv1 encodes the sketch as ASAPv1 HLL HIP.
func (h *HyperLogLogHIP) MarshalASAPv1() ([]byte, error) {
	if h.Registers == nil {
		return nil, errors.New("hll: sketch has no registers")
	}
	return marshalRegisters(asapv1.KindHLLHIP, HLLPrecision, h.Registers.AsSlice(), &[3]float64{h.kxq0, h.kxq1, h.est})
}

// UnmarshalASAPv1 replaces the sketch with one decoded from ASAPv1 HLL HIP
// bytes at precision HLLPrecision.
func (h *HyperLogLogHIP) UnmarshalASAPv1(b []byte) error {
	regs, hip, err := unmarshalRegisters(b, asapv1.KindHLLHIP, HLLPrecision)
	if err != nil {
		return err
	}
	*h = HyperLogLogHIP{Registers: storage.Vector1DFromSlice(regs), kxq0: hip[0], kxq1: hip[1], est: hip[2]}
	return nil
}
