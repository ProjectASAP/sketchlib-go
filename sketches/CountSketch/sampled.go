package countsketch

import (
	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// maxSampledRows bounds the stack buffer for admitted-row indices; Count-Sketch
// depth is always small (a few dozen). Deeper sketches fall back to the full
// update.
const maxSampledRows = 64

// UpdateStringSampledPerRow applies a NitroSketch-style PER-ROW geometric
// admission update (design-gos-unified-edge-telemetry.md §3.1/§3.2, mirroring
// asap_sketchlib src/sketch_framework/nitro.rs):
//
//   1. the sampler decides which of the d rows are admitted for THIS item, by
//      stepping its geometric skip-counter over the flattened (item,row)
//      candidate stream — d cheap steps, NO hashing;
//   2. if no row is admitted the key is NOT hashed (drop-before-hash, the CPU
//      win); otherwise the key is hashed once;
//   3. each admitted row is updated with the 1/p inverse-probability weight,
//      keeping every row estimator unbiased.
//
// Per-row (vs per-item) admission decorrelates the d row estimates, so the
// median-of-rows concentrates the sampling error into the (1−δ) guarantee
// instead of leaving it as a common-mode term (see §3.2). A nil sampler or a
// full-rate sampler (p≥1) degenerates to the plain UpdateString.
//
// The sampler is any common.RowSampler: *common.GeometricSampler (NitroSketch
// skip-sampling, stateful) or *common.ConsistentSampler (stateless hash
// decision — location-independent, see design §3.1 single-location sampling).
func (s *CountSketch) UpdateStringSampledPerRow(key string, count float64, sampler common.RowSampler) {
	if sampler == nil || sampler.P() >= 1.0 || s.Rows > maxSampledRows {
		s.UpdateString(key, count)
		return
	}

	// 1. Per-row admission (no hash).
	sampler.BeginItem()
	var admitted [maxSampledRows]int
	n := 0
	for r := 0; r < s.Rows; r++ {
		if sampler.Admit() {
			admitted[n] = r
			n++
		}
	}
	if n == 0 {
		return // no admitted row → skip the key hash entirely
	}

	// 2. Hash once.
	hashed := storage.BuildMatrixHash(common.Hash64([]byte(key)), s.Rows, s.Cols)
	scaled := count / sampler.P() // inverse-probability weight
	isPacked := hashed.Mode() == storage.MatrixHashPacked64
	packed := hashed.Lower64()

	// 3. Update admitted rows only.
	for i := 0; i < n; i++ {
		r := admitted[i]
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
	}

	// Heavy-hitter candidate tracking: the item was admitted, so record it
	// (raw count) for the Space-Saving delta path.
	if s.SS != nil {
		s.SS.Update(key, count)
	}
}
