package ddsketch

import (
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	sketch := NewDDSketch(0.01)
	// Feed in a few values spanning several buckets; ddsketch only
	// accepts strictly-positive values so any positive set works.
	for _, v := range []float64{1.0, 2.0, 5.0, 10.0, 20.0, 50.0} {
		sketch.Update(v)
	}

	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}
	// Strip the ASAPv1 envelope before feeding into the low-level unmarshal.
	kindID, payload, err := asapmsgpack.DecodeWrapper(bytes)
	if err != nil {
		t.Fatalf("DecodeWrapper: %v", err)
	}
	if len(kindID) != 1 || kindID[0] != asapmsgpack.MagicDDSketch {
		t.Fatalf("expected kind_id [0x%02x], got %v", asapmsgpack.MagicDDSketch, kindID)
	}
	state, err := asapmsgpack.UnmarshalDDSketch(payload)
	if err != nil {
		t.Fatalf("UnmarshalDDSketch: %v", err)
	}

	// Alpha recovered from gamma should round-trip within float error.
	if math.Abs(state.Alpha-0.01) > 1e-12 {
		t.Errorf("alpha: got %v, want ~0.01", state.Alpha)
	}
	// DataPoint-level metric scalars (count/sum/min/max) are no longer
	// carried on the wire; count is recoverable by summing the bucket
	// counts.
	var totalBucketed uint64
	for _, c := range state.StoreCounts {
		totalBucketed += c
	}
	if totalBucketed != 6 {
		t.Errorf("bucket sum: got %d, want 6", totalBucketed)
	}
}

func TestSerializeMsgpackEmptySketch(t *testing.T) {
	sketch := NewDDSketch(0.01)
	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}
	// Strip the ASAPv1 envelope before feeding into the low-level unmarshal.
	kindID2, payload2, err := asapmsgpack.DecodeWrapper(bytes)
	if err != nil {
		t.Fatalf("DecodeWrapper: %v", err)
	}
	if len(kindID2) != 1 || kindID2[0] != asapmsgpack.MagicDDSketch {
		t.Fatalf("expected kind_id [0x%02x], got %v", asapmsgpack.MagicDDSketch, kindID2)
	}
	state, err := asapmsgpack.UnmarshalDDSketch(payload2)
	if err != nil {
		t.Fatalf("UnmarshalDDSketch: %v", err)
	}
	if len(state.StoreCounts) != 0 {
		t.Errorf("empty store_counts: got %d elements, want 0", len(state.StoreCounts))
	}
	// An empty sketch round-trips to an empty sketch with the +Inf/-Inf
	// min/max sentinels re-derived from the (empty) bucket distribution.
	sketch2, err := DeserializeMsgpack(bytes)
	if err != nil {
		t.Fatalf("DeserializeMsgpack: %v", err)
	}
	if sketch2.Count() != 0 {
		t.Errorf("empty count: got %d, want 0", sketch2.Count())
	}
	// Updating the reconstructed empty sketch must work (sentinels seeded).
	sketch2.Update(42.0)
	if sketch2.Count() != 1 {
		t.Errorf("after Update: count=%d, want 1", sketch2.Count())
	}
}
