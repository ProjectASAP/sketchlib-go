package hll

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	"google.golang.org/protobuf/proto"
)

// SerializeRegisterDelta converts a RegisterDelta to proto-encoded bytes in one pass.
func SerializeRegisterDelta(d *RegisterDelta) ([]byte, error) {
	updates := make([]*pb.HLLRegisterUpdate, len(d.Updates))
	for i := range d.Updates {
		u := &d.Updates[i]
		updates[i] = &pb.HLLRegisterUpdate{Index: u.Index, Value: uint32(u.Value)}
	}
	return proto.Marshal(&pb.HLLDelta{Updates: updates})
}

// DeserializeRegisterDelta converts proto-encoded bytes to a RegisterDelta in one pass.
func DeserializeRegisterDelta(data []byte) (*RegisterDelta, error) {
	var msg pb.HLLDelta
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	updates := make([]RegisterUpdate, len(msg.Updates))
	for i, u := range msg.Updates {
		updates[i] = RegisterUpdate{Index: u.Index, Value: uint8(u.Value)}
	}
	return &RegisterDelta{Updates: updates}, nil
}
