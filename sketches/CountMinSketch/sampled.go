package countminsketch

import (
	"github.com/ProjectASAP/sketchlib-go/common"
)

// maxSampledRowsCMS bounds the stack buffer for admitted-row indices. CMS depth
// is always small (a few rows); deeper sketches fall back to the full insert.
const maxSampledRowsCMS = 64

// InsertWithHashSampledPerRow applies a NitroSketch-style PER-ROW geometric
// admission insert (design-gos-unified-edge-telemetry.md §3.1/§3.2, mirroring
// CountSketch.UpdateStringSampledPerRow):
//
//  1. the EXTERNAL sampler decides which of the d rows are admitted for THIS
//     item, by stepping its geometric skip-counter over the flattened
//     (item,row) candidate stream — d cheap steps, NO hashing beyond the
//     already-computed key hash;
//  2. if no row is admitted the item touches no cell (drop-before-anything);
//  3. each admitted row is incremented with the 1/p inverse-probability weight,
//     keeping every row's cell an unbiased estimate of the true +1-per-item
//     frequency.
//
// Unlike WithSampleP (whole-item admission, RAW counts stored, consumer
// rescales ×1/p at query via the stamped envelope probability), this path
// applies the 1/p weight IN-PLACE and takes an external sampler, leaving the
// sketch's own s.sampler nil. That is required for per-row sampling: because a
// different subset of rows is admitted per item, no single scalar rescale at the
// consumer can recover the counts — the correction must be per-update. The wire
// envelope therefore stays "exact" (wireSampleP()==0) and downstream does NOT
// double-correct. A nil / full-rate (p>=1) sampler degenerates to InsertWithHash.
//
// The sampler is any common.RowSampler — currently *common.GeometricSampler
// (NitroSketch skip-sampling, stateful). Admission is decided exactly once,
// upstream of serialization, so no downstream stage re-derives it.
func (s *CountMinSketch) InsertWithHashSampledPerRow(hash uint64, sampler common.RowSampler) {
	if sampler == nil || sampler.P() >= 1.0 || s.Rows > maxSampledRowsCMS {
		s.InsertWithHash(hash)
		return
	}

	// 1. Per-row admission (no cell work yet).
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
		return // no admitted row → touch nothing
	}

	// 2. Update admitted rows only, with the inverse-probability weight. The
	// per-row column derivation mirrors InsertWithHash exactly (row r reads the
	// hash window at shift r*bitsPerRow), so a sampled and an unsampled sketch
	// address identical cells.
	w := 1.0 / sampler.P()
	for i := 0; i < n; i++ {
		r := admitted[i]
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
		s.L2[r] += curr*curr - prev*prev
	}
}

// InsertWithHashAtRows applies a PRE-DECIDED per-row admission insert: the
// caller (typically an OTel SDK that already ran NitroSketch admission
// upstream of serialization) supplies admittedRows as a bitmask (bit r set
// ⇒ row r admits this occurrence) and value (the occurrence's raw
// magnitude — 1.0 for pure frequency counting, matching
// InsertWithHashSampledPerRow's implicit weight), and this updates exactly
// those rows with the value/p inverse-probability weight — the same
// column-derivation and cell update as InsertWithHashSampledPerRow's step
// 2, but the admission decision is GIVEN rather than made here: there is
// no sampler call in this method at all.
//
// admittedRows == 0 is a no-op (the caller is expected to have already
// dropped R(x)=∅ occurrences). p<=0 or p>=1 skips the rescale (weight =
// value). Matches InsertWithHashSampledPerRow's "exact envelope, no
// double-correct" contract: the weight is baked into the cell here, so
// downstream must NOT also stamp/consume a wire sample_p for this sketch.
func (s *CountMinSketch) InsertWithHashAtRows(hash uint64, value float64, admittedRows uint64, p float64) {
	if admittedRows == 0 || s.Rows > maxSampledRowsCMS {
		return
	}
	w := value
	if p > 0 && p < 1.0 {
		w = value / p
	}
	for r := 0; r < s.Rows; r++ {
		if admittedRows&(1<<uint(r)) == 0 {
			continue
		}
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
		s.L2[r] += curr*curr - prev*prev
	}
}
