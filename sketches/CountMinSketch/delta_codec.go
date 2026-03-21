package countminsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// SerializeDelta converts a Delta to proto-encoded bytes in one pass.
// The conversion Delta → pb.CountMinDelta → bytes avoids any intermediate
// copy of the cell slice: cells are mapped directly during proto construction.
func SerializeDelta(d *Delta) ([]byte, error) {
	cells := make([]*pb.CountMinCell, len(d.Cells))
	for i := range d.Cells {
		c := &d.Cells[i]
		cells[i] = &pb.CountMinCell{
			Row:    c.Row,
			Col:    c.Col,
			DCount: c.DCount,
			DSum:   c.DSum,
			DSum2:  c.DSum2,
		}
	}
	msg := &pb.CountMinDelta{
		Rows:  d.Rows,
		Cols:  d.Cols,
		Cells: cells,
		L1:    d.L1,
		L2:    d.L2,
	}
	return proto.Marshal(msg)
}

// DeserializeDelta converts proto-encoded bytes to a Delta in one pass.
// The conversion bytes → pb.CountMinDelta → Delta avoids re-allocating
// the cell slice: it is sized exactly from the proto repeated field.
func DeserializeDelta(data []byte) (*Delta, error) {
	var msg pb.CountMinDelta
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	cells := make([]CellDelta, len(msg.Cells))
	for i, c := range msg.Cells {
		cells[i] = CellDelta{
			Row:    c.Row,
			Col:    c.Col,
			DCount: c.DCount,
			DSum:   c.DSum,
			DSum2:  c.DSum2,
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
