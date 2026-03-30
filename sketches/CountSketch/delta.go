package countsketch

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// CellDelta holds the signed additive delta for a single (row, col) cell.
type CellDelta struct {
	Row, Col uint32
	DCount   float64 // signed
}

// Delta is the native Go representation of a sparse CountSketch delta.
// All fields are plain Go types; no proto dependency.
type Delta struct {
	Rows, Cols uint32
	Cells      []CellDelta // only cells where |ΔCount| ≥ threshold
	L2         []float64   // full per-row L2 deltas
	// HHKeys contains heavy-hitter candidate keys from the upstream Space-Saving
	// tracker. Replaces the old TopK (key+count) entries; downstream queries the
	// merged CS accumulator for accurate counts. No counts forwarded.
	HHKeys []string
}

// ComputeDelta computes a sparse delta between snapshot and current.
// A cell is included when |ΔCount| ≥ threshold.
// L2 row deltas are always included in full.
// HHKeys contains the Space-Saving candidates from current.SS.
// Returns an error if the two sketches have different dimensions.
func ComputeDelta(snapshot, current *CountSketch, threshold float64) (*Delta, error) {
	if snapshot.Rows != current.Rows || snapshot.Cols != current.Cols {
		return nil, fmt.Errorf("countsketch: dimension mismatch (%d×%d vs %d×%d)",
			snapshot.Rows, snapshot.Cols, current.Rows, current.Cols)
	}
	rows, cols := current.Rows, current.Cols

	d := &Delta{
		Rows:  uint32(rows),
		Cols:  uint32(cols),
		Cells: make([]CellDelta, 0, rows*cols/20), // ~5% fill hint
		L2:    make([]float64, rows),
	}

	for r := 0; r < rows; r++ {
		snapCount := snapshot.Count[r]
		curCount := current.Count[r]
		for c := 0; c < cols; c++ {
			dc := curCount[c] - snapCount[c]
			if dc != 0 && (dc <= -threshold || dc >= threshold) {
				d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
			}
		}
		d.L2[r] = current.L2[r] - snapshot.L2[r]
	}

	// Opt-4: emit Space-Saving candidates instead of TopK heap.
	if current.SS != nil && current.SS.Len() > 0 {
		d.HHKeys = current.SS.Candidates()
	}

	return d, nil
}

// ApplyDelta applies d to target using += semantics for cells/L2.
// For heavy-hitter keys, each key in d.HHKeys is queried against the updated
// target CS matrix and the result is used to rebuild target.TopK with accurate
// globally-merged estimates.
// Cells outside target's dimensions are silently skipped.
func ApplyDelta(target *CountSketch, d *Delta) {
	for i := range d.Cells {
		c := &d.Cells[i]
		r, col := int(c.Row), int(c.Col)
		if r >= target.Rows || col >= target.Cols {
			continue
		}
		target.Count[r][col] += c.DCount
	}
	for r, v := range d.L2 {
		if r < target.Rows {
			target.L2[r] += v
		}
	}
	// Rebuild TopK from hh_keys using the updated (merged) CS matrix.
	if len(d.HHKeys) > 0 && target.TopK != nil {
		for _, key := range d.HHKeys {
			est, _ := target.QueryWithHash(common.QueryFrequency, common.Hash64([]byte(key)))
			target.TopK.Update(key, int64(est))
		}
	}
}
