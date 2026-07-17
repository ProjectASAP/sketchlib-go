package countminsketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// integralCellDelta converts a float64 cell delta to its exact int64 value,
// returning ok=false when the delta is fractional or out of i64 range. The
// CMS sparse delta wire carries cells as i64 (proto d_counts is sint64), while
// the full-frame counters are f64; a fractional/weighted cell cannot be
// represented losslessly on the delta wire, so the caller rejects the delta
// and falls back to the lossless full frame rather than truncating (which
// would make a window-1 full frame and a window-2 delta-against-empty
// reconstruct to different matrices, and silently vanish |Δ|<1 cells).
func integralCellDelta(df float64) (int64, bool) {
	if math.IsNaN(df) || math.IsInf(df, 0) {
		return 0, false
	}
	r := math.Round(df)
	if r != df {
		return 0, false
	}
	if r > math.MaxInt64 || r < math.MinInt64 {
		return 0, false
	}
	return int64(r), true
}

// CellDelta holds the additive delta for a single (row, col) cell.
// DSum and DSum2 are dropped: the receiver reconstructs Sum and Sum2
// from DCount for unweighted (unit-weight) streams (Sum=Sum2=Count).
type CellDelta struct {
	Row, Col uint32
	DCount   int64
}

// Delta is the native Go representation of a sparse CountMinSketch delta.
// All fields are plain Go types; no proto dependency.
//
// The shape mirrors the CountSketch Delta so the two sketches serialize to
// structurally identical wire frames (see CountMinDelta vs CountSketchDelta in
// the protos). HHKeys is the optional heavy-hitter-keys channel: CMS can track
// heavy hitters, but whether HHKeys is populated is a control-plane decision.
// It is empty when no heavy-hitter source is wired to the producing sketch.
type Delta struct {
	Rows, Cols uint32
	Cells      []CellDelta // only cells where |ΔCount| ≥ threshold
	L1         []float64   // full per-row L1 norm deltas (one entry per row)
	// HHKeys contains heavy-hitter candidate keys from an upstream tracker
	// (e.g. a Space-Saving sketch), mirroring CountSketch's Delta.HHKeys.
	// Empty when heavy-hitter tracking is not enabled. Downstream queries the
	// merged CMS matrix for each key to (re)build its Top-K with accurate,
	// globally-merged estimates. No counts are forwarded.
	HHKeys []string
}

// HeavyHitterSource supplies heavy-hitter candidate keys for delta emission.
// A CountMinSketch does not maintain such a tracker itself; the control plane
// wires one in via ComputeDeltaWithHH when heavy-hitter tracking is enabled.
// This mirrors CountSketch's upstream Space-Saving tracker (SS.Candidates()).
type HeavyHitterSource interface {
	Candidates() []string
}

// ComputeDelta computes a sparse delta between snapshot and current.
// A cell is included when |ΔCount| ≥ threshold.
// L1 row deltas are always included in full (negligible size).
// Heavy-hitter keys are not emitted (HHKeys stays empty); use
// ComputeDeltaWithHH to forward candidates from an upstream tracker.
// Returns an error if the two sketches have different dimensions.
func ComputeDelta(snapshot, current *CountMinSketch, threshold float64) (*Delta, error) {
	return ComputeDeltaWithHH(snapshot, current, threshold, nil)
}

// ComputeDeltaWithHH computes a sparse delta between snapshot and current and
// conditionally forwards heavy-hitter candidate keys.
//
// A cell is included when |ΔCount| ≥ threshold; L1 row deltas are always
// included in full. When hh is non-nil and reports candidates, those keys are
// copied into Delta.HHKeys (mirroring CountSketch's ComputeDelta, which reads
// current.SS.Candidates()). When hh is nil — the default for a plain CMS with
// no heavy-hitter tracker — HHKeys stays empty and the serialized bytes are
// byte-identical to a sketch that never tracked heavy hitters.
//
// Returns an error if the two sketches have different dimensions.
func ComputeDeltaWithHH(snapshot, current *CountMinSketch, threshold float64, hh HeavyHitterSource) (*Delta, error) {
	if snapshot.Rows != current.Rows || snapshot.Cols != current.Cols {
		return nil, fmt.Errorf("countminsketch: dimension mismatch (%d×%d vs %d×%d)",
			snapshot.Rows, snapshot.Cols, current.Rows, current.Cols)
	}
	rows, cols := current.Rows, current.Cols

	d := &Delta{
		Rows:  uint32(rows),
		Cols:  uint32(cols),
		Cells: make([]CellDelta, 0, rows*cols/20), // ~5% fill hint
		L1:    make([]float64, rows),
	}

	for r := 0; r < rows; r++ {
		snapCount := snapshot.Count[r]
		curCount := current.Count[r]

		for c := 0; c < cols; c++ {
			df := curCount[c] - snapCount[c]
			// Threshold on the float magnitude so a fractional threshold
			// behaves as written and |Δ|<1 cells are not silently dropped via
			// int truncation.
			if df == 0 || math.Abs(df) < threshold {
				continue
			}
			dc, ok := integralCellDelta(df)
			if !ok {
				// i64 cell wire (proto d_counts sint64) cannot carry a
				// fractional/weighted delta losslessly; reject so the caller
				// emits the lossless full frame instead of truncating.
				return nil, fmt.Errorf(
					"countminsketch: ComputeDelta: cell (%d,%d) delta %v is non-integral; "+
						"the sparse delta wire is integer-only (use the full frame for "+
						"weighted/fractional sketches)", r, c, df)
			}
			d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
		}
		d.L1[r] = current.L1[r] - snapshot.L1[r]
	}

	// Control-plane-gated heavy-hitter emission. Only populated when a source
	// is wired in (CMS has no built-in tracker), so the field is empty for a
	// plain CMS — keeping cross-language byte parity in that case.
	if hh != nil {
		if cands := hh.Candidates(); len(cands) > 0 {
			d.HHKeys = cands
		}
	}

	return d, nil
}

// ComputeDeltaAgainstEmpty computes the sparse delta of current against an
// implicit all-zero snapshot of the same dimensions, WITHOUT materializing a
// zero snapshot sketch. It is the allocation-free fast path for the
// per-window-reset (PWR) delta contract, where the cached base is always the
// empty matrix and ComputeDelta(emptyPrev, current) reduces to "encode
// current's non-zero cells".
//
// The result is byte-identical to ComputeDelta(zeroSketch, current, threshold)
// for a freshly-constructed zeroSketch of current's dimensions: the same
// threshold test, the same integral-cell rejection, the same cell ordering
// (row-major), and L1 carried in full (== current's, since the base is
// zero). Callers SerializeDelta the result exactly as for ComputeDelta.
func ComputeDeltaAgainstEmpty(current *CountMinSketch, threshold float64) (*Delta, error) {
	rows, cols := current.Rows, current.Cols
	d := &Delta{
		Rows:  uint32(rows),
		Cols:  uint32(cols),
		Cells: make([]CellDelta, 0, rows*cols/20), // ~5% fill hint
		L1:    make([]float64, rows),
	}
	for r := 0; r < rows; r++ {
		curCount := current.Count[r]
		for c := 0; c < cols; c++ {
			df := curCount[c] // snapshot cell is 0
			if df == 0 || math.Abs(df) < threshold {
				continue
			}
			dc, ok := integralCellDelta(df)
			if !ok {
				return nil, fmt.Errorf(
					"countminsketch: ComputeDeltaAgainstEmpty: cell (%d,%d) delta %v is "+
						"non-integral; the sparse delta wire is integer-only", r, c, df)
			}
			d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
		}
		d.L1[r] = current.L1[r] // snapshot.L1[r] == 0
	}
	return d, nil
}

// HeavyHitterSink receives heavy-hitter (key, count) estimates rebuilt from a
// delta's HHKeys after the matrix has been merged. It mirrors CountSketch's
// receiver-side Top-K rebuild (target.TopK.Update). A plain CMS has no such
// sink, so callers that want heavy-hitter rebuild pass one via ApplyDeltaWithHH.
type HeavyHitterSink interface {
	Update(key string, count int64)
}

// ApplyDelta applies d to target using += semantics.
// For unweighted streams, Sum and Sum2 equal Count, so all three arrays
// are incremented by DCount (identical to the FO full-payload behaviour).
// Cells outside target's dimensions are silently skipped.
// HHKeys are not consumed here (a plain CMS has no Top-K sink); use
// ApplyDeltaWithHH to rebuild heavy hitters from them.
func ApplyDelta(target *CountMinSketch, d *Delta) {
	ApplyDeltaWithHH(target, d, nil)
}

// ApplyDeltaWithHH applies d to target (identical cell/norm semantics to
// ApplyDelta) and, when sink is non-nil, rebuilds heavy hitters from d.HHKeys.
//
// Mirroring CountSketch's receiver path, each key is queried against the freshly
// merged CMS matrix and the resulting estimate is pushed into the sink. When
// sink is nil — the default for a plain CMS with no Top-K — HHKeys are simply
// carried on the decoded Delta and not otherwise consumed.
func ApplyDeltaWithHH(target *CountMinSketch, d *Delta, sink HeavyHitterSink) {
	for i := range d.Cells {
		c := &d.Cells[i]
		r, col := int(c.Row), int(c.Col)
		if r >= target.Rows || col >= target.Cols {
			continue
		}
		target.Count[r][col] += float64(c.DCount)
		target.Sum[r][col] += float64(c.DCount)
		target.Sum2[r][col] += float64(c.DCount)
	}
	for r, v := range d.L1 {
		if r < target.Rows {
			target.L1[r] += v
		}
	}
	// Rebuild heavy hitters from hh_keys using the updated (merged) CMS matrix.
	if sink != nil && len(d.HHKeys) > 0 {
		for _, key := range d.HHKeys {
			est, _ := target.QueryWithHash(common.QueryFrequency, common.Hash64([]byte(key)))
			sink.Update(key, int64(est))
		}
	}
}
