package countsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes.
// HHKeys are written to the hh_keys field (Opt-4). The legacy topk field is
// not populated.
func SerializeDelta(d *Delta) ([]byte, error) {
	cells := make([]*pb.CountSketchCell, len(d.Cells))
	for i := range d.Cells {
		c := &d.Cells[i]
		cells[i] = &pb.CountSketchCell{Row: c.Row, Col: c.Col, DCount: c.DCount}
	}
	msg := &pb.CountSketchDelta{
		Rows:   d.Rows,
		Cols:   d.Cols,
		Cells:  cells,
		L2:     d.L2,
		HhKeys: d.HHKeys,
		// Topk intentionally omitted (replaced by hh_keys).
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta.
// Reads hh_keys (Opt-4) when present; falls back to legacy topk entries
// (converting them to key-only HHKeys) for backward compatibility.
func DeserializeDelta(data []byte) (*Delta, error) {
	var msg pb.CountSketchDelta
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	cells := make([]CellDelta, len(msg.Cells))
	for i, c := range msg.Cells {
		cells[i] = CellDelta{Row: c.Row, Col: c.Col, DCount: c.DCount}
	}
	d := &Delta{
		Rows:  msg.Rows,
		Cols:  msg.Cols,
		Cells: cells,
		L2:    msg.L2,
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
