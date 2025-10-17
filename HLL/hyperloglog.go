// Original implementation from DataFusion, in Rust:
// https://github.com/apache/datafusion/blob/182d5dc5e456322664da921f446018a0549e60bc/datafusion/functions-aggregate/src/hyperloglog.rs#L71
// Algorithm from:
// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
// https://arxiv.org/abs/1702.01284
package hll

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	common "github.com/approx-telemetry/sketchlib-go/common"
)

const (
	// HLLPrecision controls the number of registers. Higher values improve accuracy at the cost of memory.
	HLLPrecision = 14

	// HLLRegisterBits is the number of hash bits inspected for leading zeros.
	HLLRegisterBits = 64 - HLLPrecision

	// HLLRegisterCount is the total number of registers maintained by the sketch.
	HLLRegisterCount = 1 << HLLPrecision

	// HLLRegisterMask isolates the register index from a hashed value.
	HLLRegisterMask = HLLRegisterCount - 1
)

// HyperLogLog is a probabilistic data structure that estimates the cardinality of a multiset.
type HyperLogLog struct {
	// Registers hold the per-bucket leading-zero counts.
	Registers [HLLRegisterCount]uint8
}

// Debug prints the raw register state for inspection.
func (h *HyperLogLog) Debug() {
	fmt.Println(h.Registers)
}

// NewHyperLogLog returns a new, zero-initialized HyperLogLog sketch.
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{}
}

// Insert adds the value x to the sketch.
func (h *HyperLogLog) Insert(x float64) {
	buf := common.Float64ToBytes(x)
	hash := common.HashIt(0, buf)
	index := int(hash & HLLRegisterMask)
	w := hash >> HLLPrecision
	leadingZeros := uint8(bits.LeadingZeros64(w)-HLLPrecision) + 1
	maxLeadingZeros := uint8(HLLRegisterBits) + 1
	if leadingZeros > maxLeadingZeros {
		leadingZeros = maxLeadingZeros
	}
	if h.Registers[index] < leadingZeros {
		h.Registers[index] = leadingZeros
	}
}

// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
// https://arxiv.org/abs/1702.01284
func (h *HyperLogLog) hllSigma(x float64) float64 {
	if x == 1.0 {
		return math.Inf(1)
	}
	y := 1.0
	z := x
	for {
		x *= x
		zPrime := z
		z += x * y
		y += y
		if zPrime == z {
			break
		}
	}
	return z
}

// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
// https://arxiv.org/abs/1702.01284
func (h *HyperLogLog) hllTau(x float64) float64 {
	if x == 0.0 || x == 1.0 {
		return 0.0
	}

	y := 1.0
	z := 1.0 - x
	for {
		x = math.Sqrt(x)
		zPrime := z
		y *= 0.5
		z -= math.Pow(1.0-x, 2) * y
		if zPrime == z {
			break
		}
	}
	return z / 3.0
}

// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
// https://arxiv.org/abs/1702.01284
func (h *HyperLogLog) getHistogram() [HLLRegisterBits + 2]uint32 {
	var histogram [HLLRegisterBits + 2]uint32
	for _, r := range h.Registers {
		histogram[r]++
	}
	return histogram
}

// Estimate returns the estimated cardinality of the multiset.
func (h *HyperLogLog) Estimate() int {
	histogram := h.getHistogram()
	m := float64(HLLRegisterCount)
	z := m * h.hllTau((m-float64(histogram[HLLRegisterBits+1]))/m)
	for i := HLLRegisterBits; i >= 1; i-- {
		z += float64(histogram[i])
		z *= 0.5
	}
	z += m * h.hllSigma(float64(histogram[0])/m)
	return int(math.Round(0.5 / math.Ln2 * m * m / z))
}

// Merge combines other into h. Sketches must share the same precision.
func (h *HyperLogLog) Merge(other *HyperLogLog) error {
	if len(h.Registers) != len(other.Registers) {
		return errors.New("hyperloglog: incompatible register lengths for merging")
	}
	for i := 0; i < HLLRegisterCount; i++ {
		if other.Registers[i] > h.Registers[i] {
			h.Registers[i] = other.Registers[i]
		}
	}
	return nil
}
