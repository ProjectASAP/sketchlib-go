package countminsketch

import "fmt"

// CellDelta holds the additive deltas for a single (row, col) cell.
type CellDelta struct {
	Row, Col            uint32
	DCount, DSum, DSum2 float64
}

// Delta is the native Go representation of a sparse CountMinSketch delta.
// All fields are plain Go types; no proto dependency.
type Delta struct {
	Rows, Cols uint32
	Cells      []CellDelta // only cells where |ΔCount|+|ΔSum|+|ΔSum2| ≥ threshold
	L1, L2     []float64   // full per-row norm deltas (one entry per row)
}

// ComputeDelta computes a sparse delta between snapshot and current.
// A cell is included when |ΔCount|+|ΔSum|+|ΔSum2| ≥ threshold.
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
		snapSum := snapshot.Sum[r]
		snapSum2 := snapshot.Sum2[r]
		curCount := current.Count[r]
		curSum := current.Sum[r]
		curSum2 := current.Sum2[r]

		for c := 0; c < cols; c++ {
			dc := curCount[c] - snapCount[c]
			ds := curSum[c] - snapSum[c]
			ds2 := curSum2[c] - snapSum2[c]
			mag := dc
			if mag < 0 {
				mag = -mag
			}
			if s := ds; s < 0 {
				mag += -s
			} else {
				mag += s
			}
			if s := ds2; s < 0 {
				mag += -s
			} else {
				mag += s
			}
			if mag >= threshold {
				d.Cells = append(d.Cells, CellDelta{
					Row: uint32(r), Col: uint32(c),
					DCount: dc, DSum: ds, DSum2: ds2,
				})
			}
		}
		d.L1[r] = current.L1[r] - snapshot.L1[r]
		d.L2[r] = current.L2[r] - snapshot.L2[r]
	}

	return d, nil
}

// ApplyDelta applies d to target using += semantics.
// Cells outside target's dimensions are silently skipped.
func ApplyDelta(target *CountMinSketch, d *Delta) {
	for i := range d.Cells {
		c := &d.Cells[i]
		r, col := int(c.Row), int(c.Col)
		if r >= target.Rows || col >= target.Cols {
			continue
		}
		target.Count[r][col] += c.DCount
		target.Sum[r][col] += c.DSum
		target.Sum2[r][col] += c.DSum2
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
