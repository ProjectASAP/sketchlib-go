// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package hll

import "github.com/ProjectASAP/sketchlib-go/common"

// InsertWithHashReportingChange performs the identical register update as
// InsertWithHash (register-wise max: registers[index] = max(registers[index],
// leadingZeros)) but ALSO reports which register was touched and its (old,
// new) values.
//
// This is the primitive ASAPCollector's Layer-D GOS register-change adapter
// (asap-precompute-go's HLLWrapper) is built on top of
// (sampling-cdm-gos-derivations.md §8.7). It is deliberately narrower than
// CountSketch's UpdateStringGOS: CountSketch touches every row on every
// insert and so can embed its own threshold check in one call; an HLL
// insert touches AT MOST ONE register, and only when the candidate
// leading-zero count exceeds the register's CURRENT value. This method
// reports exactly that mechanical "did the register change, and to what"
// fact (mirroring the existing UpdateRegister/RegisterValue accessors) —
// the GOS threshold math itself (comparing the register's linearized 2^C
// contribution against the value it was last SENT, which is policy state
// HLL must track separately because registers are never reset) belongs in
// the caller, not here.
//
// changed=false (oldVal==newVal) means the insert was a no-op for this
// register: the candidate leading-zero count did not exceed the register's
// existing value. Hash-threshold sampling (WithSampleP) is honored
// identically to InsertWithHash: a hash rejected by the sampler reports
// changed=false without touching any register.
//
// A sparse-backed instance is promoted to dense as a side effect (via
// RegisterValue/UpdateRegister, exactly as every other register-level
// accessor already does) — this primitive does not itself add a new
// dense-promotion path, but callers that want to preserve a sparse
// instance's memory profile should not route GOS-gated inserts through it.
func (h *HyperLogLog) InsertWithHashReportingChange(hash uint64) (index int, oldVal, newVal uint8, changed bool) {
	if h.sampleP > 0 && h.sampleP < 1.0 && !common.KeepKeyByThreshold(hash, h.sampleP) {
		return 0, 0, 0, false
	}
	idx, lz := h.IndexAndLZ(hash)
	old := h.RegisterValue(idx)
	nv, ch := h.UpdateRegister(idx, lz)
	return idx, old, uint8(nv), ch
}

// UpdateValueReportingChange is the GOS-aware counterpart to UpdateValue: it
// hashes v with the SAME CanonicalHashSeed UpdateValue uses, then delegates
// to InsertWithHashReportingChange — so a caller (e.g. HLLWrapper) can
// detect and report per-register changes for float-valued observations
// without duplicating HLL's hash computation.
func (h *HyperLogLog) UpdateValueReportingChange(v float64) (index int, oldVal, newVal uint8, changed bool) {
	buf := common.Float64ToBytes(v)
	hash := common.HashIt(common.CanonicalHashSeed, buf)
	return h.InsertWithHashReportingChange(hash)
}
