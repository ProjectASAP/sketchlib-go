package countminsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/countminsketch"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes using the new packed
// array encoding (Opt-1+2, Stage 2). The legacy cells_legacy field is NOT
// populated; new receivers use cell_rows/cell_cols/d_counts.
// HHKeys are written to the hh_keys field (mirroring CountSketch). When empty
// (the default for a plain CMS), the repeated field encodes to nothing, so the
// bytes are byte-identical to a delta that never carried heavy-hitter keys.
// Wire selection: when every cell delta is integral, deltas ride the compact
// packed-sint64 d_counts field (byte-identical to the pre-float encoding).
// When ANY delta is fractional (weighted / per-row 1/p sampled streams), all
// deltas ride the packed-float64 d_counts_float field instead — lossless.
func SerializeDelta(d *Delta) ([]byte, error) {
	n := len(d.Cells)
	cellRows := make([]uint32, n)
	cellCols := make([]uint32, n)
	dCounts := make([]int64, 0, n)
	allInt := true
	for i := range d.Cells {
		c := &d.Cells[i]
		cellRows[i] = c.Row
		cellCols[i] = c.Col
		if allInt {
			if iv, ok := integralCellDelta(c.DValue); ok {
				dCounts = append(dCounts, iv)
			} else {
				allInt = false
			}
		}
	}
	msg := &pb.CountMinDelta{
		Rows:     d.Rows,
		Cols:     d.Cols,
		CellRows: cellRows,
		CellCols: cellCols,
		L1:       d.L1,
		HhKeys:   d.HHKeys,
	}
	if allInt {
		msg.DCounts = dCounts
	} else {
		floats := make([]float64, n)
		for i := range d.Cells {
			floats[i] = d.Cells[i].DValue
		}
		msg.DCountsFloat = floats
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta.
// Reads new packed arrays when present (len(CellRows) > 0); falls back to
// cells_legacy for old producers. Reads hh_keys symmetrically (mirroring
// CountSketch); they are carried on the returned Delta for callers that wire a
// heavy-hitter sink via ApplyDeltaWithHH.
func DeserializeDelta(data []byte) (*Delta, error) {
	var msg pb.CountMinDelta
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	var cells []CellDelta
	if len(msg.CellRows) > 0 {
		// New packed encoding path. Prefer the float wire when present
		// (fractional/weighted deltas); fall back to the sint64 wire.
		n := len(msg.CellRows)
		cells = make([]CellDelta, n)
		useFloat := len(msg.DCountsFloat) == n
		for i := 0; i < n; i++ {
			var dv float64
			if useFloat {
				dv = msg.DCountsFloat[i]
			} else {
				dv = float64(msg.DCounts[i])
			}
			cells[i] = CellDelta{
				Row:    msg.CellRows[i],
				Col:    msg.CellCols[i],
				DValue: dv,
			}
		}
	} else {
		// Legacy fallback: old producers wrote repeated CountMinCell messages.
		cells = make([]CellDelta, len(msg.CellsLegacy))
		for i, c := range msg.CellsLegacy {
			cells[i] = CellDelta{
				Row:    c.Row,
				Col:    c.Col,
				DValue: float64(c.DCount),
			}
		}
	}

	d := &Delta{
		Rows:  msg.Rows,
		Cols:  msg.Cols,
		Cells: cells,
		L1:    msg.L1,
	}
	if keys := msg.GetHhKeys(); len(keys) > 0 {
		d.HHKeys = keys
	}
	return d, nil
}
