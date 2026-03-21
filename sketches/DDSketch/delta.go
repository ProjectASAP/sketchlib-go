package ddsketch

import (
	"math"
)

// ComputeDelta serializes the incremental change from snapshot to current as a
// compact DDSketch payload using the same gob codec as SerializeToBytes.
//
// The incremental sketch contains only buckets whose count increased (cur[k] >
// snap[k]), with DeltaCount = cur[k] − snap[k] and sum = cur.sum − snap.sum.
// This is identical to serialising the sketch of inserts that arrived this
// window, which is already compact because per-window counts are much smaller
// than accumulated counts (better gob varint compression).
//
// In window/accumulation mode all bucket counts are monotonically non-decreasing,
// so the incremental sketch is a lossless representation of the change.
// In batch mode bucket decreases are omitted (positive-only), which can cause
// slight reconstruction error; the caller should use a size-based fallback
// (send full if delta >= full) to cap the regression.
func ComputeDelta(snapshot, current *DDSketch, threshold uint64) ([]byte, error) {
	incr := &DDSketch{
		mapping: current.mapping,
		min:     math.Inf(1),
		max:     math.Inf(-1),
	}

	current.EachBucket(func(k int32, count uint64) {
		snapCount := snapshot.BucketCount(k)
		if count <= snapCount {
			return
		}
		dc := count - snapCount
		if dc < threshold {
			return
		}
		incr.AddToBucket(k, dc)
	})

	// Set sum directly (AddToBucket does not update sum).
	if current.sum > snapshot.sum {
		incr.sum = current.sum - snapshot.sum
	}

	return incr.SerializeToBytes()
}

// ApplyDelta merges a delta payload (produced by ComputeDelta) into target.
// The payload is a serialised incremental DDSketch; Merge adds its bucket
// counts, count, sum, min, and max into target.
func ApplyDelta(target *DDSketch, data []byte) error {
	incr, err := DeserializeDDSketchFromBytes(data)
	if err != nil {
		return err
	}
	return target.Merge(incr)
}
