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
func SerializeDelta(d *Delta) ([]byte, error) {
	n := len(d.Cells)
	cellRows := make([]uint32, n)
	cellCols := make([]uint32, n)
	dCounts := make([]int64, n)
	for i := range d.Cells {
		c := &d.Cells[i]
		cellRows[i] = c.Row
		cellCols[i] = c.Col
		dCounts[i] = c.DCount
	}
	msg := &pb.CountMinDelta{
		Rows:     d.Rows,
		Cols:     d.Cols,
		CellRows: cellRows,
		CellCols: cellCols,
		DCounts:  dCounts,
		L1:       d.L1,
		L2:       d.L2,
		HhKeys:   d.HHKeys,
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
		// New packed encoding path.
		n := len(msg.CellRows)
		cells = make([]CellDelta, n)
		for i := 0; i < n; i++ {
			cells[i] = CellDelta{
				Row:    msg.CellRows[i],
				Col:    msg.CellCols[i],
				DCount: msg.DCounts[i],
			}
		}
	} else {
		// Legacy fallback: old producers wrote repeated CountMinCell messages.
		cells = make([]CellDelta, len(msg.CellsLegacy))
		for i, c := range msg.CellsLegacy {
			cells[i] = CellDelta{
				Row:    c.Row,
				Col:    c.Col,
				DCount: int64(c.DCount),
			}
		}
	}

	d := &Delta{
		Rows:  msg.Rows,
		Cols:  msg.Cols,
		Cells: cells,
		L1:    msg.L1,
		L2:    msg.L2,
	}
	if keys := msg.GetHhKeys(); len(keys) > 0 {
		d.HHKeys = keys
	}
	return d, nil
}
