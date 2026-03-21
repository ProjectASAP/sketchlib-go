package countsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes in one pass.
func SerializeDelta(d *Delta) ([]byte, error) {
	cells := make([]*pb.CountSketchCell, len(d.Cells))
	for i := range d.Cells {
		c := &d.Cells[i]
		cells[i] = &pb.CountSketchCell{Row: c.Row, Col: c.Col, DCount: c.DCount}
	}
	msg := &pb.CountSketchDelta{
		Rows:  d.Rows,
		Cols:  d.Cols,
		Cells: cells,
		L2:    d.L2,
	}
	if len(d.TopK) > 0 {
		entries := make([]*pb.HeapEntry, len(d.TopK))
		for i := range d.TopK {
			entries[i] = &pb.HeapEntry{Key: d.TopK[i].Key, Count: float64(d.TopK[i].Count)}
		}
		msg.Topk = &pb.TopKState{K: d.TopKK, Entries: entries}
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta in one pass.
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
	if msg.Topk != nil && len(msg.Topk.Entries) > 0 {
		d.TopKK = msg.Topk.K
		d.TopK = make([]TopKEntry, len(msg.Topk.Entries))
		for i, e := range msg.Topk.Entries {
			d.TopK[i] = TopKEntry{Key: e.Key, Count: int64(e.Count)}
		}
	}
	return d, nil
}
