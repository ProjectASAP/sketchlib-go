package countminsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/countminsketch"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes using the new packed
// array encoding (Opt-1+2, Stage 2). The legacy cells_legacy field is NOT
// populated; new receivers use cell_rows/cell_cols/d_counts.
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
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta.
// Reads new packed arrays when present (len(CellRows) > 0); falls back to
// cells_legacy for old producers.
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

	return &Delta{
		Rows:  msg.Rows,
		Cols:  msg.Cols,
		Cells: cells,
		L1:    msg.L1,
		L2:    msg.L2,
	}, nil
}
