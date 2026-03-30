package countsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes using the new packed
// array encoding (Opt-2, Stage 2). The legacy cells_legacy field is NOT
// populated; new receivers use cell_rows/cell_cols/d_counts and l2.
// HHKeys are written to the hh_keys field (Opt-4). The legacy topk field is
// not populated.
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
	msg := &pb.CountSketchDelta{
		Rows:     d.Rows,
		Cols:     d.Cols,
		CellRows: cellRows,
		CellCols: cellCols,
		DCounts:  dCounts,
		L2:       d.L2,
		HhKeys:   d.HHKeys,
		// Topk intentionally omitted (replaced by hh_keys).
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta.
// Reads new packed arrays when present (len(CellRows) > 0); falls back to
// cells_legacy for old producers. Reads hh_keys (Opt-4) when present; falls
// back to legacy topk entries for backward compatibility.
func DeserializeDelta(data []byte) (*Delta, error) {
	var msg pb.CountSketchDelta
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
		// Legacy fallback: old producers wrote repeated CountSketchCell messages.
		cells = make([]CellDelta, len(msg.CellsLegacy))
		for i, c := range msg.CellsLegacy {
			cells[i] = CellDelta{Row: c.Row, Col: c.Col, DCount: int64(c.DCount)}
		}
	}

	// Use new l2 field; fall back to l2_legacy for old producers.
	l2 := msg.L2
	if len(l2) == 0 {
		l2 = msg.L2Legacy
	}

	d := &Delta{
		Rows:  msg.Rows,
		Cols:  msg.Cols,
		Cells: cells,
		L2:    l2,
	}
	if keys := msg.GetHhKeys(); len(keys) > 0 {
		d.HHKeys = keys
	} else if topk := msg.GetTopk(); topk != nil && len(topk.GetEntries()) > 0 {
		// Legacy fallback: extract keys from old-style TopK entries.
		d.HHKeys = make([]string, len(topk.GetEntries()))
		for i, e := range topk.GetEntries() {
			d.HHKeys[i] = e.GetKey()
		}
	}
	return d, nil
}
