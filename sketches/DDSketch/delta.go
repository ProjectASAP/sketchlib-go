package ddsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/ddsketch"
	"google.golang.org/protobuf/proto"
)

// ComputeDelta computes a sparse delta between snapshot and current.
// A bucket is included when Δcount ≥ threshold.
//
// The DataPoint-level metric scalars (count/sum/min/max deltas) are no longer
// carried on the wire: the receiver recovers the count delta by summing the
// applied bucket deltas and derives min/max from the resulting bucket
// distribution (relative-accuracy bounded).
// Returns proto-marshalled DDSketchDelta bytes.
func ComputeDelta(snapshot, current *DDSketch, threshold uint64) ([]byte, error) {
	delta := &pb.DDSketchDelta{}

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
//
// The DataPoint-level metric scalars (count/sum/min/max) are no longer carried
// on the wire: the count delta is recovered by summing the applied bucket
// deltas and min/max are derived from each touched bucket's representative
// value (relative-accuracy bounded). sum is unrecoverable and is not updated.
func ApplyDelta(target *DDSketch, data []byte) error {
	var delta pb.DDSketchDelta
	if err := proto.Unmarshal(data, &delta); err != nil {
		return err
	}

	for _, b := range delta.Buckets {
		// ensure() returns the actual write index — b.Index's own bucket,
		// unless target.store is maxBins-capped and b.Index falls below the
		// collapsed floor (not the case for an unbounded backend-side
		// target, but AddToBucket/addOne route through the same call, so
		// this stays correct if a capped target is ever used here too).
		idx := target.store.ensure(b.Index)
		target.store.counts.AsMutSlice()[idx] += b.DCount
		target.count += b.DCount
		// Derive min/max from the representative of the bucket ACTUALLY
		// written to (see AddToBucket).
		rep := target.mapping.Value(target.store.offset + int32(idx))
		if rep < target.min {
			target.min = rep
		}
		if rep > target.max {
			target.max = rep
		}
	}

	return nil
}
