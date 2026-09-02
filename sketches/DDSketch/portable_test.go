package ddsketch

import (
	"math"
	"testing"

	ddpb "github.com/ProjectASAP/sketchlib-go/proto/ddsketch"
	"google.golang.org/protobuf/proto"
)

// TestSerializeStateProtoBytes_RoundTrip pins the new
// `SerializeStateProtoBytes` → `NewFromStateProtoBytes` round trip
// used by the DataCollector ddsketchprocessor migration to sketchlib-go.
// Also verifies that the raw bytes decode via the proto type directly
// (no envelope wrapper), which is the shape ASAPQuery-backend's
// `DdSketchAccumulator::from_sketchlib_proto_bytes` consumes.
func TestSerializeStateProtoBytes_RoundTrip(t *testing.T) {
	orig := NewDDSketch(0.01)
	for _, v := range []float64{1.0, 2.0, 5.0, 10.0, 20.0, 50.0} {
		orig.Update(v)
	}

	bytes, err := orig.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes: %v", err)
	}
	if len(bytes) == 0 {
		t.Fatal("bytes empty")
	}

	// Decode as raw DDSketchState (no envelope) — the shape the
	// Rust consumer expects.
	var state ddpb.DDSketchState
	if err := proto.Unmarshal(bytes, &state); err != nil {
		t.Fatalf("proto.Unmarshal into DDSketchState: %v", err)
	}
	if math.Abs(state.Alpha-0.01) > 1e-12 {
		t.Errorf("alpha: got %v, want ~0.01", state.Alpha)
	}
	// DataPoint-level metric scalars (count/sum/min/max) are no longer
	// carried on the wire; count is recoverable by summing the bucket
	// counts.
	var totalBucketed uint64
	for _, c := range state.GetStoreCounts() {
		totalBucketed += c
	}
	if totalBucketed != 6 {
		t.Errorf("bucket sum: got %d, want 6", totalBucketed)
	}

	// And via the Go convenience constructor.
	reconstructed, err := NewFromStateProtoBytes(bytes)
	if err != nil {
		t.Fatalf("NewFromStateProtoBytes: %v", err)
	}
	if reconstructed.Count() != orig.Count() {
		t.Errorf("count: got %d, want %d", reconstructed.Count(), orig.Count())
	}
	// Quantile estimate should match within alpha tolerance.
	origP50, _ := orig.Quantile(0.5)
	recP50, _ := reconstructed.Quantile(0.5)
	if math.Abs(origP50-recP50) > origP50*0.01 {
		t.Errorf("p50: got %v, want ~%v", recP50, origP50)
	}
}

func TestNewFromState_RejectsInvalidAlpha(t *testing.T) {
	for _, bad := range []float64{0.0, 1.0, -0.1, 1.5} {
		_, err := NewFromState(&ddpb.DDSketchState{
			Alpha: bad,
		})
		if err == nil {
			t.Errorf("alpha=%v: expected error, got nil", bad)
		}
	}
}

func TestNewFromState_NilReturnsError(t *testing.T) {
	if _, err := NewFromState(nil); err == nil {
		t.Error("expected error on nil state")
	}
}

func TestNewFromState_EmptySketchRoundTrip(t *testing.T) {
	orig := NewDDSketch(0.01)
	bytes, err := orig.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes: %v", err)
	}
	reconstructed, err := NewFromStateProtoBytes(bytes)
	if err != nil {
		t.Fatalf("NewFromStateProtoBytes: %v", err)
	}
	if reconstructed.Count() != 0 {
		t.Errorf("empty count: got %d, want 0", reconstructed.Count())
	}
	// Updating the reconstructed sketch should work — the
	// constructor must seed min/max with +Inf/-Inf sentinels.
	reconstructed.Update(42.0)
	if reconstructed.Count() != 1 {
		t.Errorf("after Update: count=%d, want 1", reconstructed.Count())
	}
}

func TestNewFromState_NonEmptyBucketsRoundTrip(t *testing.T) {
	orig := NewDDSketch(0.01)
	for _, v := range []float64{1.0, 2.0, 3.0, 5.0, 10.0} {
		orig.Update(v)
	}
	bytes, err := orig.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes: %v", err)
	}
	reconstructed, err := NewFromStateProtoBytes(bytes)
	if err != nil {
		t.Fatalf("NewFromStateProtoBytes: %v", err)
	}
	if reconstructed.Count() != orig.Count() {
		t.Errorf("count: got %d, want %d", reconstructed.Count(), orig.Count())
	}
	// Further Updates should keep working — bucket store must be
	// reattached to a live storage.Vector1D.
	reconstructed.Update(100.0)
	if reconstructed.Count() != orig.Count()+1 {
		t.Errorf("after Update: count=%d, want %d",
			reconstructed.Count(), orig.Count()+1)
	}
}

// TestSerializeStateProtoBytes_QuantilesAfterScalarRemoval is the end-to-end
// guard that DDSketch quantiles still work after the DataPoint-level metric
// scalars (count/sum/min/max) were dropped from the wire format. It inserts a
// spread of positive values, serializes to the inner proto bytes, deserializes
// (which must re-derive count and min/max from the bucket distribution alone),
// and asserts every quantile estimate is within the alpha relative-accuracy
// guarantee of the original sketch's estimate.
func TestSerializeStateProtoBytes_QuantilesAfterScalarRemoval(t *testing.T) {
	const alpha = 0.01
	orig := NewDDSketch(alpha)
	var truth []float64
	for i := 1; i <= 2000; i++ {
		v := float64(i)
		orig.Update(v)
		truth = append(truth, v)
	}

	bytes, err := orig.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes: %v", err)
	}
	rec, err := NewFromStateProtoBytes(bytes)
	if err != nil {
		t.Fatalf("NewFromStateProtoBytes: %v", err)
	}

	// count must be recovered exactly by summing the bucket counts.
	if rec.Count() != orig.Count() {
		t.Fatalf("count: got %d, want %d", rec.Count(), orig.Count())
	}

	// Each reconstructed quantile must honour the DDSketch relative-accuracy
	// guarantee against ground truth. min/max are re-derived from the bucket
	// distribution on the reconstructed side (no stored scalars), so q=0 and
	// q=1 are now bucket-representative values. With the DataDog representative
	// gamma^k*(1+alpha) the edge error is EXACTLY alpha (a value at the bottom
	// edge, e.g. 1.0 == gamma^0, has representative 1+alpha), so alpha is the
	// correct tolerance — the old 2*alpha slack was only needed for the
	// midpoint gamma^(k+0.5) representative (edge error sqrt(gamma)-1 > alpha).
	const tol = alpha + 1e-9
	for _, q := range []float64{0.0, 0.25, 0.5, 0.9, 0.99, 1.0} {
		recQ, ok := rec.Quantile(q)
		if !ok {
			t.Fatalf("rec.Quantile(%v) not ok", q)
		}
		exact := trueQuantile(truth, q)
		if exact == 0 {
			continue
		}
		re := math.Abs(recQ-exact) / exact
		if re > tol {
			t.Errorf("q=%v: reconstructed=%v truth=%v relErr=%v > tol=%v",
				q, recQ, exact, re, tol)
		}
	}
}

// TestSerializeStateProtoBytes_SparseOutlier is the regression test for
// sketchlib-go#72: a single far-outlier value must no longer force the
// full-snapshot wire format to carry a dense array spanning millions of
// mostly-empty bucket positions. It compares the wire size of a sketch with
// one huge outlier against the same sketch without it, and asserts the
// with-outlier payload stays small (proportional to the number of populated
// buckets, not the span) — proving bucketWireForm actually switched to the
// sparse `buckets` encoding rather than paying for the dense array. It also
// round-trips the sparse-encoded bytes back through NewFromStateProtoBytes
// to confirm the outlier's bucket count is not lost.
func TestSerializeStateProtoBytes_SparseOutlier(t *testing.T) {
	const alpha = 0.01

	compact := NewDDSketch(alpha)
	for _, v := range []float64{1.0, 2.0, 3.0, 5.0, 10.0} {
		compact.Update(v)
	}
	compactBytes, err := compact.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes(compact): %v", err)
	}

	withOutlier := NewDDSketch(alpha)
	for _, v := range []float64{1.0, 2.0, 3.0, 5.0, 10.0} {
		withOutlier.Update(v)
	}
	withOutlier.Update(1e18) // forces an enormous bucket-index span
	outlierBytes, err := withOutlier.SerializeStateProtoBytes()
	if err != nil {
		t.Fatalf("SerializeStateProtoBytes(withOutlier): %v", err)
	}

	// A dense encoding of this span would be tens of millions of uint64
	// entries (many megabytes even varint-packed). The sparse encoding
	// should add only a handful of bytes over the compact sketch's payload
	// (one more (index,count) pair).
	const maxGrowth = 64 // bytes; generous slack over one extra sparse entry
	if grew := len(outlierBytes) - len(compactBytes); grew > maxGrowth {
		t.Fatalf("outlier payload grew by %d bytes (outlier=%d, compact=%d); "+
			"want <= %d — dense positional array leaked onto the wire",
			grew, len(outlierBytes), len(compactBytes), maxGrowth)
	}
	if len(outlierBytes) >= 1024 {
		t.Fatalf("outlier payload is %d bytes; want a small, sparse-encoded payload", len(outlierBytes))
	}

	// Directly confirm the sparse field (not store_counts) carried the data.
	var state ddpb.DDSketchState
	if err := proto.Unmarshal(outlierBytes, &state); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}
	if len(state.GetStoreCounts()) != 0 {
		t.Errorf("expected empty StoreCounts (sparse path), got %d entries", len(state.GetStoreCounts()))
	}
	if len(state.GetBuckets()) != 6 {
		t.Errorf("expected 6 sparse bucket entries, got %d", len(state.GetBuckets()))
	}

	// Round-trip must still recover every insert, including the outlier.
	reconstructed, err := NewFromStateProtoBytes(outlierBytes)
	if err != nil {
		t.Fatalf("NewFromStateProtoBytes: %v", err)
	}
	if reconstructed.Count() != withOutlier.Count() {
		t.Errorf("count: got %d, want %d", reconstructed.Count(), withOutlier.Count())
	}
	recMax, _ := reconstructed.Max()
	if recMax < 1e17 {
		t.Errorf("outlier bucket lost on round-trip: reconstructed max=%v", recMax)
	}
}

// TestComputeApplyDelta_QuantilesAfterScalarRemoval guards the delta path:
// a delta carries only bucket counts now, and ApplyDelta must rebuild count
// and min/max from those buckets. Quantiles after applying the delta must
// match a sketch fed the same values directly.
func TestComputeApplyDelta_QuantilesAfterScalarRemoval(t *testing.T) {
	const alpha = 0.01
	snapshot := NewDDSketch(alpha)
	current := NewDDSketch(alpha)

	var truth []float64
	for i := 1; i <= 1000; i++ {
		v := float64(i)
		current.Update(v)
		truth = append(truth, v)
	}

	deltaBytes, err := ComputeDelta(snapshot, current, 1)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}

	recv := NewDDSketch(alpha)
	if err := ApplyDelta(recv, deltaBytes); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}

	if recv.Count() != current.Count() {
		t.Fatalf("count: got %d, want %d", recv.Count(), current.Count())
	}

	// Quantiles after applying the bucket-only delta must honour the
	// relative-accuracy guarantee against ground truth (count and min/max are
	// re-derived from the applied bucket counts alone). q=1 is now the
	// bucket-representative top value; with the DataDog representative
	// gamma^k*(1+alpha) the bucket-edge error is exactly alpha (see the proto
	// round-trip test for the rationale).
	const tol = alpha + 1e-9
	for _, q := range []float64{0.5, 0.9, 0.99, 1.0} {
		got, _ := recv.Quantile(q)
		exact := trueQuantile(truth, q)
		if exact == 0 {
			continue
		}
		re := math.Abs(got-exact) / exact
		if re > tol {
			t.Errorf("q=%v: applied-delta=%v truth=%v relErr=%v > tol=%v",
				q, got, exact, re, tol)
		}
	}
}
