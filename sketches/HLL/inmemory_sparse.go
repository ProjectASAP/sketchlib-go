// In-memory sparse representation for HyperLogLog.
//
// Motivation
//
// A dense HyperLogLog at p=14 always carries a 16384-byte register array plus a
// ~2KB pendingMask bitset — ~18KB per instance — even when only a handful of
// registers are non-zero. The overwhelming majority of real-world series are
// low cardinality, so most of that memory is permanently zero.
//
// This file grafts the HyperLogLog++ in-memory sparse mechanic onto the EXISTING
// estimator (Otmar Ertl, arXiv:1702.01284) and the EXISTING 64-bit hash/bit
// layout. A sparse instance stores only its non-zero registers as a sorted
// []uint32 of packed (index<<rhoBits | rho) entries plus a small unsorted insert
// buffer (the "temp set"). When the distinct non-zero count crosses a threshold,
// the instance promotes to the dense []uint8 + pendingMask representation and
// behaves exactly like a classic instance from then on.
//
// While sparse, cardinality is estimated with linear counting
// (E = m*ln(m/V), V = m - distinctNonZero), which is both cheaper and more
// accurate than the Ertl estimator in the low-cardinality regime and matches
// HyperLogLog++ behaviour. The Ertl estimator is used once dense.
//
// The on-the-wire sparse format (sparse.go / portable.go) and its cross-language
// parity with the Rust asap_sketchlib decoder are untouched: serialization
// densifies the in-memory sparse entries into the same dense register array the
// existing encoder already consumes, so the emitted proto is byte-identical to a
// dense instance with the same registers.

package hll

import (
	"math"
	"sort"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// newVector1DU8 wraps a uint8 slice in the storage Vector1D used by the dense
// register array.
func newVector1DU8(data []uint8) *storage.Vector1D[uint8] {
	return storage.Vector1DFromSlice(data)
}

const (
	// sparseRhoBits is the number of low bits of a packed sparse entry that hold
	// the rho (leading-zeros+1) value. rho is in [1, HLLRegisterBits+1] = [1,51],
	// which needs 6 bits. The remaining 26 bits hold the register index, which is
	// in [0, HLLRegisterCount) = [0,16384) and needs only 14 bits — so the packed
	// (index<<6 | rho) layout fits comfortably in a uint32 at p=14.
	sparseRhoBits = 6
	sparseRhoMask = (1 << sparseRhoBits) - 1

	// sparseTempCap is the size of the unsorted insert buffer. Inserts append here
	// until it fills, then it is merge-sorted into the main sorted list (deduping
	// by max(rho) per index). This amortises the O(n) merge over sparseTempCap
	// inserts, mirroring the HyperLogLog++ temp-set design.
	sparseTempCap = 256

	// SparsePromoteThreshold is the distinct-non-zero-register count at which an
	// in-memory sparse instance promotes to dense. Each sparse entry costs 4 bytes
	// (uint32) in memory, so the sorted list alone reaches 4*threshold bytes; at
	// 4096 that is 16KB, on par with the 16384-byte dense array, and adding the
	// temp buffer + the dense pendingMask we would otherwise lazily avoid makes
	// dense the better representation beyond this point.
	//
	// This IN-MEMORY threshold is deliberately distinct from the on-the-wire
	// SparseCrossoverNonZero (6000), which is tuned for the varint wire encoding
	// (~2.7 bytes/entry) rather than the 4-byte in-memory entry.
	SparsePromoteThreshold = 4096
)

// packSparse builds a packed (index<<rhoBits | rho) entry.
func packSparse(index int, rho uint8) uint32 {
	return uint32(index)<<sparseRhoBits | uint32(rho)
}

func unpackSparse(e uint32) (index int, rho uint8) {
	return int(e >> sparseRhoBits), uint8(e & sparseRhoMask)
}

// sparseStore is the in-memory sparse register set: a sorted, de-duplicated list
// of packed entries plus a small unsorted insert buffer.
type sparseStore struct {
	sorted []uint32 // ascending by index, one entry per distinct index
	temp   []uint32 // unsorted recent inserts, flushed into sorted when full
}

func newSparseStore() *sparseStore {
	return &sparseStore{}
}

// insert records register[index] = max(register[index], rho) in the sparse set.
// It appends to the temp buffer and flushes when the buffer is full.
func (s *sparseStore) insert(index int, rho uint8) {
	s.temp = append(s.temp, packSparse(index, rho))
	if len(s.temp) >= sparseTempCap {
		s.flush()
	}
}

// flush merge-sorts the temp buffer into the sorted list, keeping max(rho) per
// index. After flush, temp is empty and sorted holds one entry per distinct
// index in ascending index order.
func (s *sparseStore) flush() {
	if len(s.temp) == 0 {
		return
	}
	// Sort temp by index asc, then rho desc, so the first occurrence of each
	// index carries its max rho.
	sort.Slice(s.temp, func(i, j int) bool {
		ai, ar := unpackSparse(s.temp[i])
		bi, br := unpackSparse(s.temp[j])
		if ai != bi {
			return ai < bi
		}
		return ar > br
	})

	// Merge the (already sorted, deduped) main list with the sorted temp list.
	merged := make([]uint32, 0, len(s.sorted)+len(s.temp))
	ai, bi := 0, 0
	lastIdx := -1
	appendMax := func(e uint32) {
		idx, rho := unpackSparse(e)
		if idx == lastIdx {
			// Same index as previous appended entry: keep the max rho.
			_, pRho := unpackSparse(merged[len(merged)-1])
			if rho > pRho {
				merged[len(merged)-1] = e
			}
			return
		}
		merged = append(merged, e)
		lastIdx = idx
	}
	for ai < len(s.sorted) && bi < len(s.temp) {
		aIdx, _ := unpackSparse(s.sorted[ai])
		bIdx, _ := unpackSparse(s.temp[bi])
		if aIdx <= bIdx {
			appendMax(s.sorted[ai])
			ai++
		} else {
			appendMax(s.temp[bi])
			bi++
		}
	}
	for ai < len(s.sorted) {
		appendMax(s.sorted[ai])
		ai++
	}
	for bi < len(s.temp) {
		appendMax(s.temp[bi])
		bi++
	}

	s.sorted = merged
	s.temp = s.temp[:0]
}

// distinctNonZero returns the number of distinct non-zero register indices.
func (s *sparseStore) distinctNonZero() int {
	s.flush()
	return len(s.sorted)
}

// forEach calls fn(index, rho) for every distinct non-zero register, in
// ascending index order.
func (s *sparseStore) forEach(fn func(index int, rho uint8)) {
	s.flush()
	for _, e := range s.sorted {
		idx, rho := unpackSparse(e)
		fn(idx, rho)
	}
}

// ── HyperLogLog sparse-mode integration ──────────────────────────────────────

// isSparse reports whether the sketch is currently in the in-memory sparse
// state (no dense register array / pendingMask allocated).
func (h *HyperLogLog) isSparse() bool { return h.sparse != nil }

// initSparse puts a freshly constructed sketch into the sparse state. Registers
// and pendingMask remain unallocated until promotion.
func (h *HyperLogLog) initSparse() {
	h.sparse = newSparseStore()
	h.Registers = nil
}

// sparseInsert applies max(register[index], rho) in the sparse store and
// promotes to dense if the distinct non-zero count reaches the threshold.
func (h *HyperLogLog) sparseInsert(index int, rho uint8) {
	h.sparse.insert(index, rho)
	// Cheap upper-bound gate: only pay for the exact distinct count (which forces
	// a flush) once the combined entry count could plausibly cross the threshold.
	if len(h.sparse.sorted)+len(h.sparse.temp) >= SparsePromoteThreshold {
		if h.sparse.distinctNonZero() >= SparsePromoteThreshold {
			h.promote()
		}
	}
}

// applyRegister applies max(register[index], rho) regardless of the current
// state. It is promotion-safe: a single sparseInsert can promote the sketch to
// dense (nilling h.sparse), so callers that apply many registers in a loop (e.g.
// Merge) must funnel each one through here rather than calling sparseInsert
// directly, which would nil-deref on the first post-promotion entry.
func (h *HyperLogLog) applyRegister(index int, rho uint8) {
	if h.isSparse() {
		h.sparseInsert(index, rho)
		return
	}
	regs := h.Registers.AsMutSlice()
	if rho > regs[index] {
		regs[index] = rho
	}
}

// promote materialises the dense []uint8 register array + pendingMask from the
// sparse entries (register = max rho per index) and drops the sparse store.
// After promotion the sketch is an ordinary dense instance.
func (h *HyperLogLog) promote() {
	if h.sparse == nil {
		return
	}
	regs := make([]uint8, HLLRegisterCount)
	h.sparse.forEach(func(index int, rho uint8) {
		if rho > regs[index] {
			regs[index] = rho
		}
	})
	h.Registers = newVector1DU8(regs)
	h.sparse = nil
	// pendingMask is a value-type fixed array embedded in the struct; it is
	// already zero. Nothing to allocate here — the OctoSketch delta path uses it
	// directly once dense.
}

// sparseLinearEstimate returns the linear-counting cardinality estimate for the
// current sparse state: E = m * ln(m / V) with V = m - distinctNonZero. This is
// the HyperLogLog++ low-cardinality estimator and is used in place of the Ertl
// estimator while sparse.
func (h *HyperLogLog) sparseLinearEstimate() int {
	m := float64(HLLRegisterCount)
	nonZero := h.sparse.distinctNonZero()
	v := m - float64(nonZero)
	if v <= 0 {
		// All registers occupied: fall back by promoting and using Ertl. In
		// practice promotion happens long before this at the threshold.
		h.promote()
		return h.Estimate()
	}
	return int(math.Round(m * math.Log(m/v)))
}

// sparseDenseRegisters returns a freshly materialised dense register array for
// the current sparse state WITHOUT promoting. Used by the serialization /
// snapshot paths so the emitted bytes are identical to a dense instance.
func (h *HyperLogLog) sparseDenseRegisters() []uint8 {
	regs := make([]uint8, HLLRegisterCount)
	h.sparse.forEach(func(index int, rho uint8) {
		if rho > regs[index] {
			regs[index] = rho
		}
	})
	return regs
}
