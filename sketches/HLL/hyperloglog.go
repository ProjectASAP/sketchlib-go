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

	common "github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
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

// HyperLogLog estimates cardinality using DataFusion-style estimator.
type HyperLogLog struct {
	// Registers store leading-zero counts per bucket.
	Registers *storage.Vector1D[uint8]
}

// NewHyperLogLog returns a new zero-initialized HLL sketch.
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{
		Registers: storage.Vector1DFromSlice(make([]uint8, HLLRegisterCount)),
	}
}

// New mirrors Rust constructor naming for the DataFusion-style variant.
func New() *HyperLogLog {
	return NewHyperLogLog()
}

// Debug prints raw register values (for inspection only).
func (h *HyperLogLog) Debug() {
	fmt.Println(h.RegisterSlice())
}

// RegisterSlice returns a direct view of register memory.
func (h *HyperLogLog) RegisterSlice() []uint8 {
	return h.Registers.AsSlice()
}

//
// -----------------------
// INSERTION PATHS
// -----------------------
//

func (h *HyperLogLog) TypeName() string {
	return "hll"
}

// InsertValue is the SLOW PATH for raw float64 values.
// It hashes the value and delegates to the fast path.
func (h *HyperLogLog) InsertValue(x float64) {
	buf := common.Float64ToBytes(x)
	hash := common.HashIt(common.CanonicalHashSeed, buf)
	h.InsertWithHash(hash)
}

// Insert implements OctoSketch.Insert — processes a SketchInput.
func (h *HyperLogLog) Insert(input *common.SketchInput) {
	if input == nil {
		return
	}
	h.InsertInput(input)
}

// InsertInput mirrors the Rust API while preserving the legacy float64 Insert.
func (h *HyperLogLog) InsertInput(input *common.SketchInput) {
	if input == nil {
		return
	}
	h.InsertWithHash(common.HashIt(common.CanonicalHashSeed, input.Bytes))
}

func (h *HyperLogLog) InsertMany(inputs []*common.SketchInput) {
	for _, input := range inputs {
		h.InsertInput(input)
	}
}

func (h *HyperLogLog) InsertManyWithHashes(hashes []uint64) {
	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}
}

// InsertWithHash is the FAST PATH (execution layer).
// It assumes the input has already been hashed.
//
// Bit layout (matches Rust DataFusion convention):
//   bits [63 .. 64-P)  → register index   (upper P = HLLPrecision bits)
//   bits [64-P-1 .. 0) → leading-zero payload (lower Q = HLLRegisterBits bits)
//
// The payload is left-aligned by shifting left P bits; the vacated low P
// bits are filled with 1s (via OR with HLLRegisterMask) so that an all-zero
// payload maps to exactly Q leading zeros, matching Rust's formula:
//   (hash << HLL_P) + HLL_P_MASK
func (h *HyperLogLog) InsertWithHash(hash uint64) {
	registers := h.Registers.AsMutSlice()
	// Upper HLLPrecision bits select the register bucket.
	index := int((hash >> HLLRegisterBits) & uint64(HLLRegisterMask))
	// Lower HLLRegisterBits bits, left-aligned and with low bits set to 1.
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
	for _, r := range h.RegisterSlice() {
		histogram[r]++
	}
	return histogram
}

// EstimateCardinality returns the estimated cardinality.
func (h *HyperLogLog) EstimateCardinality() int {
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

func (h *HyperLogLog) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryCardinality {
		return float64(h.EstimateCardinality()), nil
	}
	return 0, common.ErrUnsupportedQuery
}

//
// -----------------------
// MERGE
// -----------------------
//

// Reset clears all registers, returning the sketch to its zero state.
func (h *HyperLogLog) Reset() {
	clear(h.Registers.AsMutSlice())
}

// Merge combines another HLL into this one.
// Both sketches must use the same precision.
func (h *HyperLogLog) Merge(other common.Sketch) error {
	o, ok := other.(*HyperLogLog)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}

	if h.Registers.Len() != o.Registers.Len() {
		return errors.New("hyperloglog: incompatible register lengths")
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

// ── OctoSketch register-level accessors ──────────────────────────────────────
//
// These methods decompose InsertWithHash into observable pieces so that the
// HLLOcto adapter (sketch_framework/OctoSketch) can delegate all bit-layout
// logic and estimation logic here instead of duplicating it.

// IndexAndLZ decomposes a pre-computed hash into (registerIndex, leadingZeros)
// using the same DataFusion bit layout as InsertWithHash. Pure function.
func (h *HyperLogLog) IndexAndLZ(hash uint64) (index int, lz uint8) {
	index = int((hash >> HLLRegisterBits) & uint64(HLLRegisterMask))
	w := (hash << HLLPrecision) | uint64(HLLRegisterMask)
	lz = uint8(bits.LeadingZeros64(w)) + 1
	if max := uint8(HLLRegisterBits) + 1; lz > max {
		lz = max
	}
	return index, lz
}

// IndexAndLZFromInput hashes input.Bytes with CanonicalHashSeed then calls
// IndexAndLZ. This encapsulates the "which seed does HLL use" decision inside
// the sketch package, keeping it out of the adapter.
func (h *HyperLogLog) IndexAndLZFromInput(input *common.SketchInput) (index int, lz uint8) {
	return h.IndexAndLZ(common.HashIt(common.CanonicalHashSeed, input.Bytes))
}

// RegisterValue returns the current value of register at index.
func (h *HyperLogLog) RegisterValue(index int) uint8 {
	return h.Registers.AsSlice()[index]
}

// UpdateRegister applies max(registers[index], lz). Returns (float64(newVal),
// true) when the register increased; (float64(currentVal), false) otherwise.
func (h *HyperLogLog) UpdateRegister(index int, lz uint8) (newVal float64, changed bool) {
	regs := h.Registers.AsMutSlice()
	if lz > regs[index] {
		regs[index] = lz
		return float64(lz), true
	}
	return float64(regs[index]), false
}

// SetRegisterIfGreater writes registers[index] = val when val > registers[index].
// Used by HLLOcto.MergeDelta to apply max-semantics on received deltas.
func (h *HyperLogLog) SetRegisterIfGreater(index int, val uint8) {
	regs := h.Registers.AsMutSlice()
	if val > regs[index] {
		regs[index] = val
	}
}

// ForEachNonZeroRegister calls fn(index, val) for every register with val > 0.
// Does NOT clear registers — HLL registers are monotone. Used by HLLOcto.Flush.
func (h *HyperLogLog) ForEachNonZeroRegister(fn func(index int, val uint8)) {
	for i, v := range h.RegisterSlice() {
		if v > 0 {
			fn(i, v)
		}
	}
}

// ── CellSketch interface ───────────────────────────────────────────────────────

// NumRows returns 1 — HLL produces one (registerIndex, lz) pair per hash.
func (h *HyperLogLog) NumRows() int { return 1 }

// ColForRow returns the register index for the input.
func (h *HyperLogLog) ColForRow(input *common.SketchInput, _ int) int {
	index, _ := h.IndexAndLZFromInput(input)
	return index
}

// UpdateCell applies max(registers[col], lz).
// Returns (float64(newVal), true) when the register increased; (float64(currentVal), false) otherwise.
func (h *HyperLogLog) UpdateCell(_ int, col int, input *common.SketchInput) (float64, bool) {
	_, lz := h.IndexAndLZFromInput(input)
	return h.UpdateRegister(col, lz)
}

// ShouldEmit always returns true for HLL — any register increase is significant.
func (h *HyperLogLog) ShouldEmit(_, _ float64) bool { return true }

// BuildDelta emits the updated register value. Row is always 0; Col is the register index.
func (h *HyperLogLog) BuildDelta(_ int, col int, input *common.SketchInput) common.DeltaUpdate {
	return common.DeltaUpdate{
		Row:   0,
		Col:   col,
		Value: float64(h.RegisterValue(col)),
		Key:   input.Bytes,
	}
}

// ResetCell is a no-op for HLL — registers are monotone and must not decrease.
func (h *HyperLogLog) ResetCell(_ int, _ int) {}

// MergeDelta applies max(global[col], delta.Value) via SetRegisterIfGreater.
func (h *HyperLogLog) MergeDelta(delta common.DeltaUpdate) {
	if delta.Col < 0 || delta.Col >= HLLRegisterCount {
		return
	}
	h.SetRegisterIfGreater(delta.Col, uint8(delta.Value))
}

// Estimate returns the cardinality estimate. The input argument is ignored.
func (h *HyperLogLog) Estimate(_ *common.SketchInput) float64 {
	return float64(h.EstimateCardinality())
}

// Flush emits all non-zero registers as DeltaUpdates via ForEachNonZeroRegister.
// Registers are NOT reset — they are monotone.
func (h *HyperLogLog) Flush(emit func(common.DeltaUpdate)) {
	h.ForEachNonZeroRegister(func(index int, val uint8) {
		emit(common.DeltaUpdate{Row: 0, Col: index, Value: float64(val)})
	})
}

// SerializeToBytes serializes HyperLogLog into bytes.
func (h *HyperLogLog) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(h.RegisterSlice())
}

// DeserializeHyperLogLogFromBytes restores HyperLogLog from serialized bytes.
func DeserializeHyperLogLogFromBytes(data []byte) (*HyperLogLog, error) {
	var regs []uint8
	if err := common.DecodeFromBytes(data, &regs); err == nil {
		if len(regs) != HLLRegisterCount {
			return nil, errors.New("hyperloglog: invalid register length")
		}
		return &HyperLogLog{Registers: storage.Vector1DFromSlice(regs)}, nil
	}

	// Backward compatibility with legacy fixed-array gob payload.
	var legacyRegs [HLLRegisterCount]uint8
	if err := common.DecodeFromBytes(data, &legacyRegs); err != nil {
		return nil, err
	}
	regs = make([]uint8, HLLRegisterCount)
	copy(regs, legacyRegs[:])
	return &HyperLogLog{Registers: storage.Vector1DFromSlice(regs)}, nil
}
