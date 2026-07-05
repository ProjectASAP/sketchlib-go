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
// The sampler is any common.RowSampler: *common.GeometricSampler (NitroSketch
// skip-sampling, stateful) or *common.ConsistentSampler (stateless hash
// decision — location-independent, see design §3.1 single-location sampling).
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
