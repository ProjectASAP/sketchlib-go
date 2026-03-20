package ddsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
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
	current.EachBucket(func(k int32, count uint64) {
		snapCount := snapshot.BucketCount(k)
		dc := count - snapCount
		if dc >= threshold {
			delta.Buckets = append(delta.Buckets, &pb.DDSketchBucketDelta{
				Index:  k,
				DCount: dc,
			})
		}
	})

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
		target.AddToBucket(b.Index, b.DCount)
	}

	// Adjust count and sum directly; AddToBucket already incremented count per
	// bucket but did not add sum (DDSketch doesn't track per-bucket sums).
	// We add the d_count separately only for count tracking that AddToBucket
	// already did, so we must NOT double-count. Instead we only adjust sum.
	// Note: AddToBucket increments d.count for each bucket call, so the
	// bucket loop above already accounted for the count delta correctly.
	// We only need to add the sum delta here.
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
