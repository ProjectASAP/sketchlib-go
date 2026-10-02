package hll

import (
	"errors"
	"math"
	"math/bits"

	common "github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// HLLVariant mirrors Rust variants: Regular and DataFusion.
type HLLVariant int

const (
	HLLRegular HLLVariant = iota
	HLLDataFusion
)

// HyperLogLogVariant is a variant-aware HLL wrapper.
type HyperLogLogVariant struct {
	Registers *storage.Vector1D[uint8]
	Variant   HLLVariant
}

func NewHyperLogLogVariant(v HLLVariant) *HyperLogLogVariant {
	return &HyperLogLogVariant{
		Registers: storage.Vector1DFromSlice(make([]uint8, HLLRegisterCount)),
		Variant:   v,
	}
}

func NewRegular() *HyperLogLogVariant {
	return NewHyperLogLogVariant(HLLRegular)
}

func NewDataFusion() *HyperLogLogVariant {
	return NewHyperLogLogVariant(HLLDataFusion)
}

func (h *HyperLogLogVariant) TypeName() string { return "hll_variant" }

func (h *HyperLogLogVariant) Update(input *common.SketchInput) {
	if input == nil {
		return
	}
	h.InsertWithHash(common.HashIt(common.CanonicalHashSeed, input.Bytes))
}

func (h *HyperLogLogVariant) InsertWithHash(hash uint64) {
	registers := h.Registers.AsMutSlice()
	// Upper HLLPrecision bits select the register bucket (matches Rust convention).
	index := int((hash >> HLLRegisterBits) & uint64(HLLRegisterMask))
	w := (hash << HLLPrecision) | uint64(HLLRegisterMask)
	leadingZeros := uint8(bits.LeadingZeros64(w)) + 1
	maxLeadingZeros := uint8(HLLRegisterBits) + 1
	if leadingZeros > maxLeadingZeros {
		leadingZeros = maxLeadingZeros
	}
	if registers[index] < leadingZeros {
		registers[index] = leadingZeros
	}
}

func (h *HyperLogLogVariant) Estimate() int {
	if h.Variant == HLLRegular {
		return h.estimateRegular()
	}
	base := &HyperLogLog{Registers: h.Registers}
	return base.Estimate()
}

func (h *HyperLogLogVariant) estimateRegular() int {
	m := float64(HLLRegisterCount)
	alphaM := 0.7213 / (1.0 + 1.079/m)
	z := 0.0
	zeroCount := 0
	for _, reg := range h.Registers.AsSlice() {
		if reg == 0 {
			zeroCount++
		}
		z += math.Pow(2, -float64(reg))
	}
	est := alphaM * m * m / z
	if est <= m*2.5 && zeroCount != 0 {
		est = m * math.Log(m/float64(zeroCount))
	}
	return int(est)
}

func (h *HyperLogLogVariant) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q != common.QueryCardinality {
		return 0, common.ErrUnsupportedQuery
	}
	return float64(h.Estimate()), nil
}

func (h *HyperLogLogVariant) Merge(other common.Sketch) error {
	o, ok := other.(*HyperLogLogVariant)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	if h.Variant != o.Variant {
		return errors.New("hll variant mismatch")
	}
	self := h.Registers.AsMutSlice()
	otherRegs := o.Registers.AsSlice()
	for i := 0; i < HLLRegisterCount; i++ {
		if otherRegs[i] > self[i] {
			self[i] = otherRegs[i]
		}
	}
	return nil
}

// HyperLogLogHIP mirrors Rust HyperLogLogHIP.
type HyperLogLogHIP struct {
	Registers *storage.Vector1D[uint8]
	kxq0      float64
	kxq1      float64
	est       float64
}

func NewHIP() *HyperLogLogHIP {
	return &HyperLogLogHIP{
		Registers: storage.Vector1DFromSlice(make([]uint8, HLLRegisterCount)),
		kxq0:      float64(HLLRegisterCount),
		kxq1:      0,
		est:       0,
	}
}

func (h *HyperLogLogHIP) TypeName() string { return "hll_hip" }

func (h *HyperLogLogHIP) Update(input *common.SketchInput) {
	if input == nil {
		return
	}
	h.InsertWithHash(common.HashIt(common.CanonicalHashSeed, input.Bytes))
}

func (h *HyperLogLogHIP) InsertWithHash(hash uint64) {
	bucketNum := int((hash >> HLLRegisterBits) & HLLRegisterMask)
	leadingZero := uint8(bits.LeadingZeros64((hash<<HLLPrecision)+HLLRegisterMask) + 1)
	regs := h.Registers.AsMutSlice()
	oldValue := regs[bucketNum]
	newValue := leadingZero
	if newValue > oldValue {
		regs[bucketNum] = newValue
		h.est += float64(HLLRegisterCount) / (h.kxq0 + h.kxq1)
		if oldValue < 32 {
			h.kxq0 -= 1.0 / float64(uint64(1)<<oldValue)
		} else {
			h.kxq1 -= 1.0 / float64(uint64(1)<<oldValue)
		}
		if newValue < 32 {
			h.kxq0 += 1.0 / float64(uint64(1)<<newValue)
		} else {
			h.kxq1 += 1.0 / float64(uint64(1)<<newValue)
		}
	}
}

func (h *HyperLogLogHIP) Estimate() int { return int(h.est) }

func (h *HyperLogLogHIP) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q != common.QueryCardinality {
		return 0, common.ErrUnsupportedQuery
	}
	return float64(h.Estimate()), nil
}

func (h *HyperLogLogHIP) Merge(other common.Sketch) error {
	return errors.New("hll_hip merge is not supported in this Go port")
}
