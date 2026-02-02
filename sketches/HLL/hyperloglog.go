// Original implementation adapted from DataFusion (Rust):
// https://github.com/apache/datafusion/blob/182d5dc5e456322664da921f446018a0549e60bc/datafusion/functions-aggregate/src/hyperloglog.rs
//
// Algorithm reference:
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
	// HLLPrecision controls the number of registers.
	HLLPrecision = 14

	// Number of bits inspected for leading zeros.
	HLLRegisterBits = 64 - HLLPrecision

	// Total number of registers.
	HLLRegisterCount = 1 << HLLPrecision

	// Mask to extract register index.
	HLLRegisterMask = HLLRegisterCount - 1
)

// HyperLogLog estimates the cardinality of a multiset.
// This implementation follows a fast-path-first API design.
type HyperLogLog struct {
	// Registers store leading-zero counts per bucket.
	Registers [HLLRegisterCount]uint8
}

// NewHyperLogLog returns a new zero-initialized HLL sketch.
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{}
}

// Debug prints raw register values (for inspection only).
func (h *HyperLogLog) Debug() {
	fmt.Println(h.Registers)
}

//
// -----------------------
// INSERTION PATHS
// -----------------------
//

// Insert is the SLOW PATH.
// It hashes the input and delegates to the fast path.
func (h *HyperLogLog) Insert(x float64) {
	buf := common.Float64ToBytes(x)
	hash := common.HashIt(0, buf)
	h.InsertWithHash(hash)
}

// InsertWithHash is the FAST PATH (execution layer).
// It assumes the input has already been hashed.
func (h *HyperLogLog) InsertWithHash(hash uint64) {
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

//
// -----------------------
// ESTIMATION
// -----------------------
//

// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
func (h *HyperLogLog) hllSigma(x float64) float64 {
	if x == 1.0 {
		return math.Inf(1)
	}
	y := 1.0
	z := x
	for {
		x *= x
		zPrev := z
		z += x * y
		y += y
		if zPrev == z {
			break
		}
	}
	return z
}

// "New cardinality estimation algorithms for HyperLogLog sketches"
// Otmar Ertl, arXiv:1702.01284
func (h *HyperLogLog) hllTau(x float64) float64 {
	if x == 0.0 || x == 1.0 {
		return 0.0
	}

	y := 1.0
	z := 1.0 - x
	for {
		x = math.Sqrt(x)
		zPrev := z
		y *= 0.5
		z -= math.Pow(1.0-x, 2) * y
		if zPrev == z {
			break
		}
	}
	return z / 3.0
}

func (h *HyperLogLog) getHistogram() [HLLRegisterBits + 2]uint32 {
	var histogram [HLLRegisterBits + 2]uint32
	for _, r := range h.Registers {
		histogram[r]++
	}
	return histogram
}

// Estimate returns the estimated cardinality.
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

//
// -----------------------
// MERGE
// -----------------------
//

// Merge combines another HLL into this one.
// Both sketches must use the same precision.
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
