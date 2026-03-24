package ddsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/ddsketch"
	"google.golang.org/protobuf/proto"
)

// ComputeDelta computes a sparse delta between snapshot and current.
// A bucket is included when Δcount ≥ threshold.
// Count, sum, min, max deltas are always included because they are needed for
// correct quantile estimation on the receiver side.
// Returns proto-marshalled DDSketchDelta bytes.
func ComputeDelta(snapshot, current *DDSketch, threshold uint64) ([]byte, error) {
	delta := &pb.DDSketchDelta{
		DCount: int64(current.count) - int64(snapshot.count),
		DSum:   current.sum - snapshot.sum,
	}

	// min can only decrease; include when it did.
	if current.count > 0 && (snapshot.count == 0 || current.min < snapshot.min) {
		delta.MinChanged = true
		delta.NewMin = current.min
	}
	// max can only increase; include when it did.
	if current.count > 0 && (snapshot.count == 0 || current.max > snapshot.max) {
		delta.MaxChanged = true
		delta.NewMax = current.max
	}

	// Compute per-bucket deltas by iterating all buckets in current.
	if !current.store.IsEmpty() {
		currCounts := current.store.counts.AsSlice()
		var snapCounts []uint64
		if !snapshot.store.IsEmpty() {
			snapCounts = snapshot.store.counts.AsSlice()
		}
		for i, c := range currCounts {
			if c == 0 {
				continue
			}
			k := current.store.offset + int32(i)
			var snapCount uint64
			if snapCounts != nil {
				idx := k - snapshot.store.offset
				if idx >= 0 && int(idx) < len(snapCounts) {
					snapCount = snapCounts[idx]
				}
			}
			dc := c - snapCount
			if dc >= threshold {
				delta.Buckets = append(delta.Buckets, &pb.DDSketchBucketDelta{
					Index:  k,
					DCount: dc,
				})
			}
		}
	}

	return proto.Marshal(delta)
}

// ApplyDelta applies a proto-marshalled DDSketchDelta to target.
// Bucket deltas are applied with AddToBucket (additive).
// Count/sum are added. Min/max are applied with min/max semantics.
func ApplyDelta(target *DDSketch, data []byte) error {
	var delta pb.DDSketchDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return err
	}

	for _, b := range delta.Buckets {
		target.store.ensure(b.Index)
		target.store.counts.AsMutSlice()[int(b.Index-target.store.offset)] += b.DCount
		target.count += b.DCount
	}

	target.sum += delta.DSum

	if delta.MinChanged {
		if target.count == 0 || delta.NewMin < target.min {
			target.min = delta.NewMin
		}
	}
	if delta.MaxChanged {
		if target.count == 0 || delta.NewMax > target.max {
			target.max = delta.NewMax
		}
	}

	return nil
}
