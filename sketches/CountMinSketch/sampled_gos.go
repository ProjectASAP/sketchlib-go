package countminsketch

import (
	"github.com/ProjectASAP/sketchlib-go/common"
)

// applyGosCellAtRowCMS is the shared per-row body of the GOS-aware sampled
// inserts: increment row r's hashed cell by w, keep the L1 (and sum/sum2)
// sidecars in step, and if the new value reaches threshold, reset the COUNT
// cell to 0 (decrementing L1 to match, mirroring InsertWithHashGOS) and
// return the crossing. sum/sum2 are intentionally NOT reset — same as
// InsertWithHashGOS, they track a separate accumulator not carried in the GOS
// delta. No math.Abs (CMS counters are non-negative). A non-admitted row is
// never touched, so it cannot cross on this occurrence — which is what lets
// "sampling then GOS" compose correctly.
func (s *CountMinSketch) applyGosCellAtRowCMS(r int, hash uint64, w, threshold float64) (GOSCellUpdate, bool) {
	c := int((hash >> (uint(r) * s.bitsPerRow)) & s.mask)
	if c >= s.Cols {
		c %= s.Cols
	}
	countRow := s.countStore.RowSlice(r)
	sumRow := s.sumStore.RowSlice(r)
	sum2Row := s.sum2Store.RowSlice(r)

	prev := countRow[c]
	curr := prev + w
	countRow[c] = curr
	sumRow[c] += w
	sum2Row[c] += w
	s.L1[r] += w

	if curr >= threshold {
		countRow[c] = 0
		s.L1[r] -= curr
		return GOSCellUpdate{Row: uint32(r), Col: uint32(c), Delta: curr}, true
	}
	return GOSCellUpdate{}, false
}

// InsertWithHashSampledPerRowGOS is InsertWithHashSampledPerRow composed with
// the insert-time GOS threshold gate (design-gos-unified-edge-telemetry.md
// §11): NitroSketch per-row geometric admission exactly as
// InsertWithHashSampledPerRow, then a threshold check on each ADMITTED row's
// just-touched cell, resetting and reporting any crossing (like
// InsertWithHashGOS, but only over the admitted rows). The admitted cell
// carries the 1/p weight and the threshold sees that same value, so estimates
// stay unbiased.
//
// threshold<=0 disables the gate (plain InsertWithHashSampledPerRow, no cells).
// A nil/full-rate sampler (p>=1) or over-wide sketch degenerates to the
// un-sampled InsertWithHashGOS at weight 1.
func (s *CountMinSketch) InsertWithHashSampledPerRowGOS(
	hash uint64, sampler common.RowSampler, threshold float64,
) []GOSCellUpdate {
	if threshold <= 0 {
		s.InsertWithHashSampledPerRow(hash, sampler)
		return nil
	}
	if sampler == nil || sampler.P() >= 1.0 || s.Rows > maxSampledRowsCMS {
		return s.InsertWithHashGOS(hash, 1.0, threshold)
	}

	sampler.BeginItem()
	var admitted [maxSampledRowsCMS]int
	n := 0
	for r := 0; r < s.Rows; r++ {
		if sampler.Admit() {
			admitted[n] = r
			n++
		}
	}
	if n == 0 {
		return nil
	}

	w := 1.0 / sampler.P()
	var dirty []GOSCellUpdate
	for i := 0; i < n; i++ {
		if d, ok := s.applyGosCellAtRowCMS(admitted[i], hash, w, threshold); ok {
			dirty = append(dirty, d)
		}
	}
	return dirty
}

// InsertWithHashAtRowsGOS is InsertWithHashAtRows composed with the insert-time
// GOS threshold gate: a PRE-DECIDED per-row admission bitmask (bit r ⇒ row r
// admits) applied with the value/p weight exactly as InsertWithHashAtRows, then
// a threshold check on each admitted row's touched cell, resetting+reporting
// any crossing. This is the path an OTel SDK's NitroSketch row-admission feeds
// (ApplyAdmittedOccurrence / metricdata.RowSampledSketch); with GOS active it
// now participates in insert-time detection instead of bypassing it.
//
// admittedRows==0 or threshold<=0 falls back to InsertWithHashAtRows semantics
// (no cells). p<=0 or p>=1 skips the rescale (weight = value).
func (s *CountMinSketch) InsertWithHashAtRowsGOS(
	hash uint64, value float64, admittedRows uint64, p, threshold float64,
) []GOSCellUpdate {
	if threshold <= 0 {
		s.InsertWithHashAtRows(hash, value, admittedRows, p)
		return nil
	}
	if admittedRows == 0 || s.Rows > maxSampledRowsCMS {
		return nil
	}
	w := value
	if p > 0 && p < 1.0 {
		w = value / p
	}
	var dirty []GOSCellUpdate
	for r := 0; r < s.Rows; r++ {
		if admittedRows&(1<<uint(r)) == 0 {
			continue
		}
		if d, ok := s.applyGosCellAtRowCMS(r, hash, w, threshold); ok {
			dirty = append(dirty, d)
		}
	}
	return dirty
}
