package hll

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// ComputeRegisterDelta computes the delta between snapshot and current.
// A register is included when current[i] > snapshot[i] (HLL uses max semantics:
// once a register increases it cannot decrease, so missed decreases would cause
// permanent underestimation).
// Returns proto-marshalled HLLDelta bytes.
func ComputeRegisterDelta(snapshot, current *HyperLogLog) ([]byte, error) {
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

	return proto.Marshal(delta)
}

// ApplyRegisterDelta applies a proto-marshalled HLLDelta to target using max semantics.
// Each update sets target[index] = max(target[index], value).
func ApplyRegisterDelta(target *HyperLogLog, data []byte) error {
	var delta pb.HLLDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return err
	}

	for _, upd := range delta.Updates {
		target.SetRegisterIfGreater(int(upd.Index), uint8(upd.Value))
	}

	return nil
}
