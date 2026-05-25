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

	// OctoSketch τ-batching state (used only on worker-side instances).
	// pendingCount tracks how many registers have changed since the last
	// batch emission. pendingMask is a bitset of those register indices.
	// When pendingCount reaches τ, all dirty registers are emitted at once
	// and the dirty state is cleared. This makes τ meaningful for HLL by
	// amortising the per-register delta cost over τ register changes.
	pendingCount int32
	pendingMask  [HLLRegisterCount / 64]uint64

	// sampleP is the HLL hash-threshold sampling probability in (0,1]. 1.0 (the
	// default) means no sampling and a byte-identical wire form. When p<1, a
	// DISTINCT key is kept iff u(h(x))<p using an independent re-mix of the
	// canonical hash (NOT the register hash — using the register hash would
	// correlate the kept set with the register layout and bias the estimate).
	// Stable per key, so frequency does not affect retention. The RAW sampled
	// registers are stored; the consumer rescales cardinality ×1/p at query.
	sampleP float64
}

// NewHyperLogLog returns a new zero-initialized HLL sketch.
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{
		Registers: storage.Vector1DFromSlice(make([]uint8, HLLRegisterCount)),
		sampleP:   1.0,
	}
}

// WithSampleP enables hash-threshold element sampling at probability p in
// (0,1]. With p>=1 sampling is disabled (exact, the default). A distinct key is
// kept iff u(h(x))<p, so register writes are cut to ~p× the distinct rate; the
// RAW sampled registers are stored and the probability is stamped on the
// SketchEnvelope so the consumer rescales cardinality ×1/p at query time.
//
// HLL uses hash-threshold (value-determined) sampling, NOT geometric
// skip-sampling: a max-register update is not additive, so inverse-probability
// register updates are invalid, and per-occurrence sampling would bias distinct
// counting. Hash-threshold gives every distinct key the same Pr[kept]=p.
//
// Returns the receiver for fluent construction.
func (h *HyperLogLog) WithSampleP(p float64) *HyperLogLog {
	if p >= 1.0 || p <= 0 {
		h.sampleP = 1.0
	} else {
		h.sampleP = p
	}
	return h
}

// SampleP returns the configured sampling probability (1.0 when disabled).
func (h *HyperLogLog) SampleP() float64 {
	if h.sampleP <= 0 {
		return 1.0
	}
	return h.sampleP
}

// wireSampleP returns the value stamped on SketchEnvelope.sample_p: 0.0 (proto3
// default) when sampling is disabled so the envelope is byte-identical to the
// pre-sampling format, else the actual probability.
func (h *HyperLogLog) wireSampleP() float64 {
	if h.sampleP <= 0 || h.sampleP >= 1.0 {
		return 0.0
	}
	return h.sampleP
}

// New mirrors Rust constructor naming for the DataFusion-style variant.
func New() *HyperLogLog {
	return NewHyperLogLog()
}

// clearPending resets the pending dirty-register state.
func (h *HyperLogLog) clearPending() {
	for i := range h.pendingMask {
		h.pendingMask[i] = 0
	}
	h.pendingCount = 0
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

// UpdateValue is the SLOW PATH for raw float64 values.
// It hashes the value and delegates to the fast path.
func (h *HyperLogLog) UpdateValue(x float64) {
	buf := common.Float64ToBytes(x)
	hash := common.HashIt(common.CanonicalHashSeed, buf)
	h.InsertWithHash(hash)
}

// Update is the canonical high-level update method, mirroring Rust's
// unified API (`update`). It processes one SketchInput.
func (h *HyperLogLog) Update(input *common.SketchInput) {
	if input == nil {
		return
	}
	h.InsertWithHash(input.CanonicalHash)
}

// OctoUpdate is an alias for Update kept for the OctoSketch framework.
func (h *HyperLogLog) OctoUpdate(input *common.SketchInput) { h.Update(input) }

func (h *HyperLogLog) UpdateBatch(inputs []*common.SketchInput) {
	for _, input := range inputs {
		h.Update(input)
	}
}

func (h *HyperLogLog) UpdateHashes(hashes []uint64) {
	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}
}

// InsertWithHash is the FAST PATH (execution layer).
// It assumes the input has already been hashed.
//
// Bit layout (matches Rust DataFusion convention):
//
//	bits [63 .. 64-P)  → register index   (upper P = HLLPrecision bits)
//	bits [64-P-1 .. 0) → leading-zero payload (lower Q = HLLRegisterBits bits)
//
// The payload is left-aligned by shifting left P bits; the vacated low P
// bits are filled with 1s (via OR with HLLRegisterMask) so that an all-zero
// payload maps to exactly Q leading zeros, matching Rust's formula:
//
//	(hash << HLL_P) + HLL_P_MASK
func (h *HyperLogLog) InsertWithHash(hash uint64) {
	// Hash-threshold sampling: keep this distinct key iff u(h(x))<p. The
	// decision re-mixes the canonical hash with an independent salt so it is
	// uncorrelated with the register index/leading-zero layout below. No-op
	// (single compare, no rehash) when sampleP is 1.0 / disabled.
	if h.sampleP > 0 && h.sampleP < 1.0 && !common.KeepKeyByThreshold(hash, h.sampleP) {
		return
	}
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

// Estimate returns the estimated cardinality. Mirrors Rust's HLL
// `estimate(&self) -> usize` (sketches/hll.rs) — no key argument.
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

func (h *HyperLogLog) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryCardinality {
		return float64(h.Estimate()), nil
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
	h.clearPending()
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
	if input == nil {
		return 0, 0
	}
	return h.IndexAndLZ(input.CanonicalHash)
}

// ProcessInput is the optimized OctoSketch worker fast path.
//
// Unlike CountMin/CountSketch, HLL registers are monotone: they never reset after
// a delta is emitted, so the standard "emit when cell ≥ τ, then reset" model cannot
// reduce the delta rate for HLL. Instead, τ controls how many register changes are
// accumulated before a batch emission: when pendingCount reaches τ, all dirty
// registers (those changed since the last batch) are emitted at once and the dirty
// state is cleared. This gives the same O(1/τ) reduction in delta traffic as the
// reset-based model does for CountMin.
func (h *HyperLogLog) ProcessInput(input *common.SketchInput, tau float64, emit func(common.DeltaUpdate)) {
	if input == nil {
		return
	}
	index, lz := h.IndexAndLZ(input.CanonicalHash)
	if _, changed := h.UpdateRegister(index, lz); changed {
		h.pendingMask[index/64] |= 1 << uint(index%64)
		h.pendingCount++
		if float64(h.pendingCount) >= tau {
			// Emit all dirty registers as a single batch, then clear state.
			regs := h.Registers.AsSlice()
			for i, mask := range h.pendingMask {
				for mask != 0 {
					bit := bits.TrailingZeros64(mask)
					idx := i*64 + bit
					emit(common.DeltaUpdate{Row: 0, Col: idx, Value: float64(regs[idx])})
					mask &^= 1 << uint(bit)
				}
			}
			h.clearPending()
		}
	}
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
// Key is not set: MergeDelta uses only Col and Value, so carrying input.Bytes would
// add GC pressure without any benefit.
func (h *HyperLogLog) BuildDelta(_ int, col int, _ *common.SketchInput) common.DeltaUpdate {
	return common.DeltaUpdate{
		Row:   0,
		Col:   col,
		Value: float64(h.RegisterValue(col)),
	}
}

// ResetCell is a no-op for HLL — registers are monotone and must not decrease.
func (h *HyperLogLog) ResetCell(_ int, _ int) {}

// OctoEstimate satisfies the octosketch.OctoSketch interface. The input
// argument is ignored — HLL's canonical estimator (`Estimate()`) takes no key.
func (h *HyperLogLog) OctoEstimate(_ *common.SketchInput) float64 {
	return float64(h.Estimate())
}

// MergeDelta applies max(global[col], delta.Value) via SetRegisterIfGreater.
func (h *HyperLogLog) MergeDelta(delta common.DeltaUpdate) {
	if delta.Col < 0 || delta.Col >= HLLRegisterCount {
		return
	}
	h.SetRegisterIfGreater(delta.Col, uint8(delta.Value))
}

// Flush emits all non-zero registers as DeltaUpdates and clears the pending dirty
// state. Emitting a full register snapshot (rather than only the pending dirty
// registers) preserves snapshot semantics: a late-joining aggregator that calls
// Flush alone receives the complete current state. For the primary aggregator,
// re-sending already-delivered register values is harmless because MergeDelta
// applies max and is therefore idempotent. Registers are NOT reset — they are
// monotone.
func (h *HyperLogLog) Flush(emit func(common.DeltaUpdate)) {
	h.ForEachNonZeroRegister(func(index int, val uint8) {
		emit(common.DeltaUpdate{Row: 0, Col: index, Value: float64(val)})
	})
	h.clearPending()
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
