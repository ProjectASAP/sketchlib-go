package countsketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// ComputeDeltaMsg computes a sparse delta between snapshot and current.
// A cell (r,c) is included when |Δcount| ≥ threshold.
// L2 per-row deltas are always included.
// TopK state is always retransmitted in full (heap is small and ordering is
// non-additive, so sparse delta is not safe for TopK).
// Returns a CountSketchDelta proto message.
func ComputeDeltaMsg(snapshot, current *CountSketch, threshold float64) (*pb.CountSketchDelta, error) {
	if snapshot.Rows != current.Rows || snapshot.Cols != current.Cols {
		return nil, fmt.Errorf("countsketch: dimension mismatch (%d×%d vs %d×%d)",
			snapshot.Rows, snapshot.Cols, current.Rows, current.Cols)
	}

	delta := &pb.CountSketchDelta{
		Rows: uint32(current.Rows),
		Cols: uint32(current.Cols),
	}

	for r := 0; r < current.Rows; r++ {
		for c := 0; c < current.Cols; c++ {
			d := current.Count[r][c] - snapshot.Count[r][c]
			if math.Abs(d) >= threshold {
				delta.Cells = append(delta.Cells, &pb.CountSketchCell{
					Row:    uint32(r),
					Col:    uint32(c),
					DCount: d,
				})
			}
		}
		delta.L2 = append(delta.L2, current.L2[r]-snapshot.L2[r])
	}

	// Retransmit TopK in full whenever it is non-empty.
	if current.TopK != nil && len(current.TopK.Heap) > 0 {
		entries := make([]*pb.HeapEntry, 0, len(current.TopK.Heap))
		for _, item := range current.TopK.Heap {
			entries = append(entries, &pb.HeapEntry{
				Key:   item.Key,
				Count: float64(item.Count),
			})
		}
		delta.Topk = &pb.TopKState{
			K:       uint32(current.TopK.K),
			Entries: entries,
		}
	}

	return delta, nil
}

// ApplyDeltaMsg applies a CountSketchDelta proto message to target using += semantics.
// Cells outside the target's dimensions are silently skipped.
// TopK, if present in delta, replaces the target's TopK heap entirely.
func ApplyDeltaMsg(target *CountSketch, delta *pb.CountSketchDelta) error {
	for _, cell := range delta.Cells {
		r, c := int(cell.Row), int(cell.Col)
		if r >= target.Rows || c >= target.Cols {
			continue
		}
		target.Count[r][c] += cell.DCount
	}

	for r, dL2 := range delta.L2 {
		if r < target.Rows {
			target.L2[r] += dL2
		}
	}

	if delta.Topk != nil && len(delta.Topk.Entries) > 0 {
		target.TopK.Heap = target.TopK.Heap[:0]
		for _, e := range delta.Topk.Entries {
			target.TopK.Heap = append(target.TopK.Heap, common.Item{
				Key:   e.Key,
				Count: int64(e.Count),
			})
		}
	}

	return nil
}

// SerializeDeltaBytes marshals a CountSketchDelta proto message to bytes.
func SerializeDeltaBytes(delta *pb.CountSketchDelta) ([]byte, error) {
	return proto.Marshal(delta)
}

// DeserializeDeltaBytes unmarshals bytes into a CountSketchDelta proto message.
func DeserializeDeltaBytes(data []byte) (*pb.CountSketchDelta, error) {
	var delta pb.CountSketchDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return nil, err
	}
	return &delta, nil
}

// ComputeDelta computes a sparse delta and returns proto-marshalled bytes.
// Kept for backward compatibility. Falls back to full serialization on dimension mismatch.
func ComputeDelta(snapshot, current *CountSketch, threshold float64) ([]byte, error) {
	msg, err := ComputeDeltaMsg(snapshot, current, threshold)
	if err != nil {
		// Dimension mismatch — fall back to full serialization.
		return current.SerializeToBytes()
	}
	return SerializeDeltaBytes(msg)
}

// ApplyDelta applies a proto-marshalled CountSketchDelta to target using += semantics.
// Kept for backward compatibility.
func ApplyDelta(target *CountSketch, data []byte) error {
	msg, err := DeserializeDeltaBytes(data)
	if err != nil {
		return err
	}
	return ApplyDeltaMsg(target, msg)
}
