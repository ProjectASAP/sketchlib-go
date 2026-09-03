package countsketch

import (
	"math/bits"

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
//  1. the sampler decides which of the d rows are admitted for THIS item, by
//     stepping its geometric skip-counter over the flattened (item,row)
//     candidate stream — d cheap steps, NO hashing;
//  2. if no row is admitted the key is NOT hashed (drop-before-hash, the CPU
//     win); otherwise the key is hashed once;
//  3. each admitted row is updated with the 1/p inverse-probability weight,
//     keeping every row estimator unbiased.
//
// Per-row (vs per-item) admission decorrelates the d row estimates, so the
// median-of-rows concentrates the sampling error into the (1−δ) guarantee
// instead of leaving it as a common-mode term (see §3.2). A nil sampler or a
// full-rate sampler (p≥1) degenerates to the plain UpdateString.
//
// The sampler is any common.RowSampler — currently *common.GeometricSampler
// (NitroSketch skip-sampling, stateful). Admission is decided exactly once,
// upstream of serialization, so no downstream stage re-derives it.
func (s *CountSketch) UpdateStringSampledPerRow(key string, count float64, sampler common.RowSampler) {
	if sampler == nil || sampler.P() >= 1.0 || s.Rows > maxSampledRows {
		s.UpdateString(key, count)
		return
	}

	// 1. Direct geometric jump over this item's row block (no hash).
	admittedRows := common.AdmitRows(sampler, s.Rows)
	if admittedRows == 0 {
		return // no admitted row → skip the key hash entirely
	}

	// 2. Hash once.
	hashed := storage.BuildMatrixHash(common.Hash64([]byte(key)), s.Rows, s.Cols)
	scaled := count / sampler.P() // inverse-probability weight
	isPacked := hashed.Mode() == storage.MatrixHashPacked64
	packed := hashed.Lower64()

	// 3. Update admitted rows only.
	for admittedRows != 0 {
		r := bits.TrailingZeros64(admittedRows)
		admittedRows &^= uint64(1) << uint(r)
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

// UpdateStringAtRows applies a PRE-DECIDED per-row admission update: the
// caller (typically an OTel SDK that already ran NitroSketch admission
// upstream of serialization — see design-gos-unified-edge-telemetry.md
// §3.1/§3.2) supplies admittedRows as a bitmask (bit r set ⇒ row r
// admits this occurrence), and this hashes the key ONCE and updates
// exactly those rows with the 1/p inverse-probability weight — the same
// hash-once-then-update-admitted-rows shape as UpdateStringSampledPerRow's
// steps 2-3, but the admission decision is GIVEN rather than made here:
// there is no sampler call in this method at all.
//
// admittedRows == 0 (R(x)=∅) is a no-op — the caller is expected to have
// already dropped such occurrences before they ever reach here (mirrors
// UpdateStringSampledPerRow's n==0 early return being the decision point,
// not this one). p<=0 or p>=1 skips the rescale (weight 1). Rows beyond
// the 64-bit bitmask budget (s.Rows > 64) are a no-op — same ceiling
// UpdateStringSampledPerRow enforces via maxSampledRows, just returning
// rather than falling back to a full update (there is no "full update"
// concept here: admission was already decided elsewhere).
func (s *CountSketch) UpdateStringAtRows(key string, count float64, admittedRows uint64, p float64) {
	if admittedRows == 0 || s.Rows > maxSampledRows {
		return
	}
	hashed := storage.BuildMatrixHash(common.Hash64([]byte(key)), s.Rows, s.Cols)
	scaled := count
	if p > 0 && p < 1.0 {
		scaled = count / p
	}
	isPacked := hashed.Mode() == storage.MatrixHashPacked64
	packed := hashed.Lower64()

	for r := 0; r < s.Rows; r++ {
		if admittedRows&(1<<uint(r)) == 0 {
			continue
		}
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
	if s.SS != nil {
		s.SS.Update(key, count)
	}
}
