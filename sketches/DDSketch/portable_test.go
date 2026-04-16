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
		orig.Add(v)
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
	if state.Count != 6 {
		t.Errorf("count: got %d, want 6", state.Count)
	}
	if state.Sum != 88.0 {
		t.Errorf("sum: got %v, want 88", state.Sum)
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
	origP50, _ := orig.GetValueAtQuantile(0.5)
	recP50, _ := reconstructed.GetValueAtQuantile(0.5)
	if math.Abs(origP50-recP50) > origP50*0.01 {
		t.Errorf("p50: got %v, want ~%v", recP50, origP50)
	}
}

func TestNewFromState_RejectsInvalidAlpha(t *testing.T) {
	for _, bad := range []float64{0.0, 1.0, -0.1, 1.5} {
		_, err := NewFromState(&ddpb.DDSketchState{
			Alpha: bad,
			Count: 1,
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
	// Adding to the reconstructed sketch should work — the
	// constructor must seed min/max with +Inf/-Inf sentinels.
	reconstructed.Add(42.0)
	if reconstructed.Count() != 1 {
		t.Errorf("after Add: count=%d, want 1", reconstructed.Count())
	}
}

func TestNewFromState_NonEmptyBucketsRoundTrip(t *testing.T) {
	orig := NewDDSketch(0.01)
	for _, v := range []float64{1.0, 2.0, 3.0, 5.0, 10.0} {
		orig.Add(v)
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
	// Further Adds should keep working — bucket store must be
	// reattached to a live storage.Vector1D.
	reconstructed.Add(100.0)
	if reconstructed.Count() != orig.Count()+1 {
		t.Errorf("after Add: count=%d, want %d",
			reconstructed.Count(), orig.Count()+1)
	}
}
