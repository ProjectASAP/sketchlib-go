package countsketch

import (
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// applyGosCellAtRow is the shared per-row body of the GOS-aware sampled
// inserts: update row r's hashed cell by sign·scaled, keep the L2 sidecar in
// step, and if the new magnitude reaches threshold, reset the cell to 0 (again
// adjusting L2) and return the crossing. This is exactly the per-row inner
// loop of UpdateStringGOS, factored so the sampled variants (per-row geometric
// admission and pre-decided row-admission) apply the identical detect+reset to
// whichever subset of rows they admit — a non-admitted row is never touched, so
// it cannot cross on this occurrence, which is what makes "sampling then GOS"
// compose correctly: the threshold check sees the same 1/p-upweighted cell
// value it would have accumulated, only on the rows that actually moved.
func (s *CountSketch) applyGosCellAtRow(
	r int, hashed storage.MatrixHashType, packed uint64, isPacked bool, scaled, threshold float64,
) (GOSCellUpdate, bool) {
	var c int
	var sign float64
	if isPacked {
		c, sign = s.fastPacked64PosAndSign(packed, r)
	} else {
		c, sign = s.derivePosAndSignFromHashed(hashed, r)
	}
	row := s.Count[r]
	prev := row[c]
	curr := prev + sign*scaled
	row[c] = curr
	s.L2[r] += (curr * curr) - (prev * prev)
	if math.Abs(curr) >= threshold {
		row[c] = 0
		s.L2[r] -= curr * curr
		return GOSCellUpdate{Row: uint32(r), Col: uint32(c), Delta: curr}, true
	}
	return GOSCellUpdate{}, false
}

// UpdateStringSampledPerRowGOS is UpdateStringSampledPerRow composed with the
// insert-time GOS threshold gate (design-gos-unified-edge-telemetry.md §11): it
// runs NitroSketch per-row geometric admission exactly as
// UpdateStringSampledPerRow, then checks each ADMITTED row's just-touched cell
// against threshold, resetting and reporting any crossing (like
// UpdateStringGOS, but only over the admitted rows). Estimates stay unbiased —
// the admitted cell carries the 1/p weight and the threshold sees that same
// value.
//
// threshold<=0 disables the gate and behaves exactly like
// UpdateStringSampledPerRow (no cells returned). A nil/full-rate sampler (p>=1)
// or an over-wide sketch degenerates to the un-sampled UpdateStringGOS, so
// toggling either knob never needs a different call site.
func (s *CountSketch) UpdateStringSampledPerRowGOS(
	key string, count float64, sampler common.RowSampler, threshold float64,
) []GOSCellUpdate {
	if threshold <= 0 {
		s.UpdateStringSampledPerRow(key, count, sampler)
		return nil
	}
	if sampler == nil || sampler.P() >= 1.0 || s.Rows > maxSampledRows {
		return s.UpdateStringGOS(key, count, threshold)
	}

	// 1. Direct geometric jump — identical decisions to UpdateStringSampledPerRow.
	admittedRows := common.AdmitRows(sampler, s.Rows)
	if admittedRows == 0 {
		return nil // no admitted row → skip the key hash entirely
	}

	// 2. Hash once, 3. update+gate admitted rows only.
	hashed := storage.BuildMatrixHash(common.Hash64([]byte(key)), s.Rows, s.Cols)
	scaled := count / sampler.P()
	isPacked := hashed.Mode() == storage.MatrixHashPacked64
	packed := hashed.Lower64()

	var dirty []GOSCellUpdate
	for admittedRows != 0 {
		r := bits.TrailingZeros64(admittedRows)
		admittedRows &^= uint64(1) << uint(r)
		if d, ok := s.applyGosCellAtRow(r, hashed, packed, isPacked, scaled, threshold); ok {
			dirty = append(dirty, d)
		}
	}
	if s.SS != nil {
		s.SS.Update(key, count)
	}
	return dirty
}

// UpdateStringAtRowsGOS is UpdateStringAtRows composed with the insert-time GOS
// threshold gate: it applies a PRE-DECIDED per-row admission bitmask (bit r ⇒
// row r admits) with the 1/p weight, exactly as UpdateStringAtRows, then checks
// each admitted row's touched cell against threshold and resets+reports any
// crossing. This is the path an OTel SDK's NitroSketch row-admission feeds
// (ApplyAdmittedOccurrence / metricdata.RowSampledSketch); with GOS active it
// now participates in insert-time detection instead of bypassing it.
//
// admittedRows==0 or threshold<=0 falls back to UpdateStringAtRows semantics
// (no cells returned). p<=0 or p>=1 skips the rescale (weight 1). Rows beyond
// the 64-bit bitmask budget are a no-op, same ceiling as UpdateStringAtRows.
func (s *CountSketch) UpdateStringAtRowsGOS(
	key string, count float64, admittedRows uint64, p, threshold float64,
) []GOSCellUpdate {
	if threshold <= 0 {
		s.UpdateStringAtRows(key, count, admittedRows, p)
		return nil
	}
	if admittedRows == 0 || s.Rows > maxSampledRows {
		return nil
	}
	hashed := storage.BuildMatrixHash(common.Hash64([]byte(key)), s.Rows, s.Cols)
	scaled := count
	if p > 0 && p < 1.0 {
		scaled = count / p
	}
	isPacked := hashed.Mode() == storage.MatrixHashPacked64
	packed := hashed.Lower64()

	var dirty []GOSCellUpdate
	for admittedRows != 0 {
		r := bits.TrailingZeros64(admittedRows)
		admittedRows &^= uint64(1) << uint(r)
		if d, ok := s.applyGosCellAtRow(r, hashed, packed, isPacked, scaled, threshold); ok {
			dirty = append(dirty, d)
		}
	}
	if s.SS != nil {
		s.SS.Update(key, count)
	}
	return dirty
}
