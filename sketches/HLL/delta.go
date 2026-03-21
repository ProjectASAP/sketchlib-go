package hll

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// ComputeRegisterDeltaMsg computes the delta between snapshot and current.
// A register is included when current[i] > snapshot[i] (HLL uses max semantics:
// once a register increases it cannot decrease, so missed decreases would cause
// permanent underestimation).
// Returns an HLLDelta proto message.
func ComputeRegisterDeltaMsg(snapshot, current *HyperLogLog) (*pb.HLLDelta, error) {
	snapRegs := snapshot.RegisterSlice()
	currRegs := current.RegisterSlice()

	delta := &pb.HLLDelta{}
	n := len(currRegs)
	if len(snapRegs) < n {
		n = len(snapRegs)
	}

	for i := 0; i < n; i++ {
		if currRegs[i] > snapRegs[i] {
			delta.Updates = append(delta.Updates, &pb.HLLRegisterUpdate{
				Index: uint32(i),
				Value: uint32(currRegs[i]),
			})
		}
	}
	// If current has more registers than snapshot (shouldn't happen at fixed
	// precision, but guard anyway), include all non-zero extra registers.
	for i := n; i < len(currRegs); i++ {
		if currRegs[i] > 0 {
			delta.Updates = append(delta.Updates, &pb.HLLRegisterUpdate{
				Index: uint32(i),
				Value: uint32(currRegs[i]),
			})
		}
	}

	return delta, nil
}

// ApplyRegisterDeltaMsg applies an HLLDelta proto message to target using max semantics.
// Each update sets target[index] = max(target[index], value).
func ApplyRegisterDeltaMsg(target *HyperLogLog, delta *pb.HLLDelta) error {
	for _, upd := range delta.Updates {
		target.SetRegisterIfGreater(int(upd.Index), uint8(upd.Value))
	}
	return nil
}

// SerializeRegisterDeltaBytes marshals an HLLDelta proto message to bytes.
func SerializeRegisterDeltaBytes(delta *pb.HLLDelta) ([]byte, error) {
	return proto.Marshal(delta)
}

// DeserializeRegisterDeltaBytes unmarshals bytes into an HLLDelta proto message.
func DeserializeRegisterDeltaBytes(data []byte) (*pb.HLLDelta, error) {
	var delta pb.HLLDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return nil, err
	}
	return &delta, nil
}

// ComputeRegisterDelta computes the register delta and returns proto-marshalled bytes.
// Kept for backward compatibility.
func ComputeRegisterDelta(snapshot, current *HyperLogLog) ([]byte, error) {
	msg, err := ComputeRegisterDeltaMsg(snapshot, current)
	if err != nil {
		return nil, err
	}
	return SerializeRegisterDeltaBytes(msg)
}

// ApplyRegisterDelta applies a proto-marshalled HLLDelta to target using max semantics.
// Kept for backward compatibility.
func ApplyRegisterDelta(target *HyperLogLog, data []byte) error {
	msg, err := DeserializeRegisterDeltaBytes(data)
	if err != nil {
		return err
	}
	return ApplyRegisterDeltaMsg(target, msg)
}
