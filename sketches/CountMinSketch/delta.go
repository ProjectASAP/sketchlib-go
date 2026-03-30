package countminsketch

import "fmt"

// CellDelta holds the additive delta for a single (row, col) cell.
// DSum and DSum2 are dropped: the receiver reconstructs Sum and Sum2
// from DCount for unweighted (unit-weight) streams (Sum=Sum2=Count).
type CellDelta struct {
	Row, Col uint32
	DCount   int64
}

// Delta is the native Go representation of a sparse CountMinSketch delta.
// All fields are plain Go types; no proto dependency.
type Delta struct {
	Rows, Cols uint32
	Cells      []CellDelta // only cells where |ΔCount| ≥ threshold
	L1, L2     []float64   // full per-row norm deltas (one entry per row)
}

// ComputeDelta computes a sparse delta between snapshot and current.
// A cell is included when |ΔCount| ≥ threshold.
// L1/L2 row deltas are always included in full (negligible size).
// Returns an error if the two sketches have different dimensions.
func ComputeDelta(snapshot, current *CountMinSketch, threshold float64) (*Delta, error) {
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
		L2:    make([]float64, rows),
	}

	for r := 0; r < rows; r++ {
		snapCount := snapshot.Count[r]
		curCount := current.Count[r]

		for c := 0; c < cols; c++ {
			dc := int64(curCount[c] - snapCount[c])
			if dc < 0 && float64(-dc) >= threshold {
				d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
			} else if dc > 0 && float64(dc) >= threshold {
				d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
			}
		}
		d.L1[r] = current.L1[r] - snapshot.L1[r]
		d.L2[r] = current.L2[r] - snapshot.L2[r]
	}

	return d, nil
}

// ApplyDelta applies d to target using += semantics.
// For unweighted streams, Sum and Sum2 equal Count, so all three arrays
// are incremented by DCount (identical to the FO full-payload behaviour).
// Cells outside target's dimensions are silently skipped.
func ApplyDelta(target *CountMinSketch, d *Delta) {
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
	for r, v := range d.L2 {
		if r < target.Rows {
			target.L2[r] += v
		}
	}
}
