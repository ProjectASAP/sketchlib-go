package countminsketch

import (
	"fmt"
	"math"

	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// ComputeDeltaMsg computes a sparse delta between snapshot and current.
// A cell (r,c) is included when |Δcount| + |Δsum| + |Δsum2| ≥ threshold.
// L1/L2 per-row deltas are always included.
// Returns the CountMinDelta proto message.
// If snapshot and current have different dimensions the caller should fall
// back to a full serialization; this function returns an error in that case.
func ComputeDeltaMsg(snapshot, current *CountMinSketch, threshold float64) (*pb.CountMinDelta, error) {
	if snapshot.Rows != current.Rows || snapshot.Cols != current.Cols {
		return nil, fmt.Errorf("countminsketch: dimension mismatch (%d×%d vs %d×%d)",
			snapshot.Rows, snapshot.Cols, current.Rows, current.Cols)
	}

	delta := &pb.CountMinDelta{
		Rows: uint32(current.Rows),
		Cols: uint32(current.Cols),
	}

	for r := 0; r < current.Rows; r++ {
		for c := 0; c < current.Cols; c++ {
			dCount := current.Count[r][c] - snapshot.Count[r][c]
			dSum := current.Sum[r][c] - snapshot.Sum[r][c]
			dSum2 := current.Sum2[r][c] - snapshot.Sum2[r][c]
			if math.Abs(dCount)+math.Abs(dSum)+math.Abs(dSum2) >= threshold {
				delta.Cells = append(delta.Cells, &pb.CountMinCell{
					Row:    uint32(r),
					Col:    uint32(c),
					DCount: dCount,
					DSum:   dSum,
					DSum2:  dSum2,
				})
			}
		}
		delta.L1 = append(delta.L1, current.L1[r]-snapshot.L1[r])
		delta.L2 = append(delta.L2, current.L2[r]-snapshot.L2[r])
	}

	return delta, nil
}

// ApplyDeltaMsg applies a CountMinDelta proto message to target using += semantics.
// Cells outside the target's dimensions are silently skipped.
func ApplyDeltaMsg(target *CountMinSketch, delta *pb.CountMinDelta) error {
	for _, cell := range delta.Cells {
		r, c := int(cell.Row), int(cell.Col)
		if r >= target.Rows || c >= target.Cols {
			continue
		}
		target.Count[r][c] += cell.DCount
		target.Sum[r][c] += cell.DSum
		target.Sum2[r][c] += cell.DSum2
	}

	for r, dL1 := range delta.L1 {
		if r < target.Rows {
			target.L1[r] += dL1
		}
	}
	for r, dL2 := range delta.L2 {
		if r < target.Rows {
			target.L2[r] += dL2
		}
	}

	return nil
}

// SerializeDeltaBytes marshals a CountMinDelta proto message to bytes.
func SerializeDeltaBytes(delta *pb.CountMinDelta) ([]byte, error) {
	return proto.Marshal(delta)
}

// DeserializeDeltaBytes unmarshals bytes into a CountMinDelta proto message.
func DeserializeDeltaBytes(data []byte) (*pb.CountMinDelta, error) {
	var delta pb.CountMinDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return nil, err
	}
	return &delta, nil
}

// ComputeDelta computes a sparse delta and returns proto-marshalled bytes.
// Kept for backward compatibility. Falls back to full serialization on dimension mismatch.
func ComputeDelta(snapshot, current *CountMinSketch, threshold float64) ([]byte, error) {
	msg, err := ComputeDeltaMsg(snapshot, current, threshold)
	if err != nil {
		// Dimension mismatch — fall back to full serialization.
		return current.SerializeToBytes()
	}
	return SerializeDeltaBytes(msg)
}

// ApplyDelta applies a proto-marshalled CountMinDelta to target using += semantics.
// Kept for backward compatibility.
func ApplyDelta(target *CountMinSketch, data []byte) error {
	msg, err := DeserializeDeltaBytes(data)
	if err != nil {
		return err
	}
	return ApplyDeltaMsg(target, msg)
}
