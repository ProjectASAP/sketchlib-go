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
		sketch.Add(v)
	}

	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}

	state, err := asapmsgpack.UnmarshalDDSketch(bytes)
	if err != nil {
		t.Fatalf("UnmarshalDDSketch: %v", err)
	}

	// Alpha recovered from gamma should round-trip within float error.
	if math.Abs(state.Alpha-0.01) > 1e-12 {
		t.Errorf("alpha: got %v, want ~0.01", state.Alpha)
	}
	if state.Count != 6 {
		t.Errorf("count: got %d, want 6", state.Count)
	}
	// sum of 1+2+5+10+20+50 = 88
	if state.Sum != 88.0 {
		t.Errorf("sum: got %v, want 88", state.Sum)
	}
	if state.Min != 1.0 {
		t.Errorf("min: got %v, want 1.0", state.Min)
	}
	if state.Max != 50.0 {
		t.Errorf("max: got %v, want 50.0", state.Max)
	}
	// Bucket counts should sum to `count`.
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
	state, err := asapmsgpack.UnmarshalDDSketch(bytes)
	if err != nil {
		t.Fatalf("UnmarshalDDSketch: %v", err)
	}
	if state.Count != 0 {
		t.Errorf("empty count: got %d, want 0", state.Count)
	}
	if len(state.StoreCounts) != 0 {
		t.Errorf("empty store_counts: got %d elements, want 0", len(state.StoreCounts))
	}
	// Empty-sketch sentinels match the Rust `DdSketch::new(alpha)` constructor.
	if !math.IsInf(state.Min, 1) {
		t.Errorf("empty min: got %v, want +Inf", state.Min)
	}
	if !math.IsInf(state.Max, -1) {
		t.Errorf("empty max: got %v, want -Inf", state.Max)
	}
}
