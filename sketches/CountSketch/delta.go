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

// TopKEntry is a single heavy-hitter entry retransmitted in full each flush.
type TopKEntry struct {
	Key   string
	Count int64
}

// Delta is the native Go representation of a sparse CountSketch delta.
// All fields are plain Go types; no proto dependency.
type Delta struct {
	Rows, Cols uint32
	Cells      []CellDelta // only cells where |ΔCount| ≥ threshold
	L2         []float64   // full per-row L2 deltas
	TopK       []TopKEntry // TopK is always retransmitted in full (non-additive)
	TopKK      uint32      // K parameter of the heap
}

// ComputeDelta computes a sparse delta between snapshot and current.
// A cell is included when |ΔCount| ≥ threshold.
// L2 row deltas and TopK are always included in full.
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
			if dc < -threshold || dc > threshold {
				d.Cells = append(d.Cells, CellDelta{Row: uint32(r), Col: uint32(c), DCount: dc})
			}
		}
		d.L2[r] = current.L2[r] - snapshot.L2[r]
	}

	if current.TopK != nil && len(current.TopK.Heap) > 0 {
		d.TopKK = uint32(current.TopK.K)
		d.TopK = make([]TopKEntry, len(current.TopK.Heap))
		for i, item := range current.TopK.Heap {
			d.TopK[i] = TopKEntry{Key: item.Key, Count: item.Count}
		}
	}

	return d, nil
}

// itemFrom converts a TopKEntry back to a common.Item.
func itemFrom(key string, count int64) common.Item { return common.Item{Key: key, Count: count} }

// ApplyDelta applies d to target using += semantics for cells/L2 and full
// replacement for TopK (which is non-additive).
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
	if len(d.TopK) > 0 && target.TopK != nil {
		target.TopK.Heap = target.TopK.Heap[:0]
		for _, e := range d.TopK {
			target.TopK.Heap = append(target.TopK.Heap, itemFrom(e.Key, e.Count))
		}
	}
}
