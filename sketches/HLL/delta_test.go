package hll

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func hllInsert(h *HyperLogLog, key string) {
	h.InsertWithHash(common.Hash64([]byte(key)))
}

// hllRegistersEqual checks that two HLL sketches have identical register arrays.
func hllRegistersEqual(t *testing.T, label string, a, b *HyperLogLog) {
	t.Helper()
	ar := a.RegisterSlice()
	br := b.RegisterSlice()
	if len(ar) != len(br) {
		t.Fatalf("%s: register count mismatch %d vs %d", label, len(ar), len(br))
	}
	for i := range ar {
		if ar[i] != br[i] {
			t.Fatalf("%s: register[%d] want %d got %d", label, i, ar[i], br[i])
		}
	}
}

// TestHLLDelta_RoundTrip verifies that ApplyRegisterDelta(ComputeRegisterDelta(snap, current))
// applied to a clone of snap reconstructs current exactly.
func TestHLLDelta_RoundTrip(t *testing.T) {
	snap := NewHyperLogLog()
	for i := 0; i < 500; i++ {
		hllInsert(snap, fmt.Sprintf("item-%d", i))
	}

	current := NewHyperLogLog()
	if err := current.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i := 500; i < 1000; i++ {
		hllInsert(current, fmt.Sprintf("item-%d", i))
	}

	delta := ComputeRegisterDelta(snap, current)

	reconstructed := NewHyperLogLog()
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge snap: %v", err)
	}
	ApplyRegisterDelta(reconstructed, delta)

	hllRegistersEqual(t, "RoundTrip", current, reconstructed)
}

// TestHLLDelta_Codec verifies the full bytes->RegisterDelta->ApplyRegisterDelta pipeline.
func TestHLLDelta_Codec(t *testing.T) {
	snap := NewHyperLogLog()
	for i := 0; i < 300; i++ {
		hllInsert(snap, fmt.Sprintf("u-%d", i))
	}

	current := NewHyperLogLog()
	if err := current.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i := 300; i < 600; i++ {
		hllInsert(current, fmt.Sprintf("u-%d", i))
	}

	delta := ComputeRegisterDelta(snap, current)
	b, err := SerializeRegisterDelta(delta)
	if err != nil {
		t.Fatalf("SerializeRegisterDelta: %v", err)
	}
	decoded, err := DeserializeRegisterDelta(b)
	if err != nil {
		t.Fatalf("DeserializeRegisterDelta: %v", err)
	}

	reconstructed := NewHyperLogLog()
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyRegisterDelta(reconstructed, decoded)

	hllRegistersEqual(t, "Codec", current, reconstructed)
}

// TestHLLDelta_MaxSemantics verifies that re-applying the same delta is
// idempotent (max semantics: applying twice is same as applying once).
func TestHLLDelta_MaxSemantics(t *testing.T) {
	snap := NewHyperLogLog()
	for i := 0; i < 200; i++ {
		hllInsert(snap, fmt.Sprintf("v-%d", i))
	}

	current := NewHyperLogLog()
	if err := current.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i := 200; i < 400; i++ {
		hllInsert(current, fmt.Sprintf("v-%d", i))
	}

	delta := ComputeRegisterDelta(snap, current)
	b, _ := SerializeRegisterDelta(delta)

	recv := NewHyperLogLog()
	if err := recv.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Apply twice — result must be same as applying once.
	d1, _ := DeserializeRegisterDelta(b)
	ApplyRegisterDelta(recv, d1)
	d2, _ := DeserializeRegisterDelta(b)
	ApplyRegisterDelta(recv, d2)

	hllRegistersEqual(t, "MaxSemantics(idempotent)", current, recv)
}

// TestHLLDelta_EmptyDelta checks that identical sketches produce zero updates.
func TestHLLDelta_EmptyDelta(t *testing.T) {
	s := NewHyperLogLog()
	for i := 0; i < 100; i++ {
		hllInsert(s, fmt.Sprintf("w-%d", i))
	}
	delta := ComputeRegisterDelta(s, s)
	if len(delta.Updates) != 0 {
		t.Fatalf("expected 0 updates for identical HLL, got %d", len(delta.Updates))
	}
}

// TestHLLDelta_CardinalityConvergence verifies that after applying deltas the
// cardinality estimate matches the direct merge result within HLL error bounds.
func TestHLLDelta_CardinalityConvergence(t *testing.T) {
	const n = 10_000
	snap := NewHyperLogLog()
	for i := 0; i < n/2; i++ {
		hllInsert(snap, fmt.Sprintf("item-%d", i))
	}

	current := NewHyperLogLog()
	if err := current.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i := n / 2; i < n; i++ {
		hllInsert(current, fmt.Sprintf("item-%d", i))
	}

	delta := ComputeRegisterDelta(snap, current)
	b, _ := SerializeRegisterDelta(delta)
	decoded, _ := DeserializeRegisterDelta(b)

	recv := NewHyperLogLog()
	if err := recv.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyRegisterDelta(recv, decoded)

	// Registers must be identical — cardinality follows automatically.
	hllRegistersEqual(t, "CardinalityConvergence", current, recv)

	got := recv.Estimate(nil)
	t.Logf("true=%d estimated=%.0f", n, got)
}

// TestHLLDelta_MultipleWindows simulates consecutive delta transmissions.
func TestHLLDelta_MultipleWindows(t *testing.T) {
	sender := NewHyperLogLog()
	receiver := NewHyperLogLog()
	snap := NewHyperLogLog()

	for w := 0; w < 5; w++ {
		for i := 0; i < 200; i++ {
			hllInsert(sender, fmt.Sprintf("w%d-item%d", w, i))
		}
		delta := ComputeRegisterDelta(snap, sender)
		b, _ := SerializeRegisterDelta(delta)
		decoded, _ := DeserializeRegisterDelta(b)
		ApplyRegisterDelta(receiver, decoded)

		snap = NewHyperLogLog()
		if err := snap.Merge(sender); err != nil {
			t.Fatalf("snap.Merge: %v", err)
		}
	}

	hllRegistersEqual(t, "MultipleWindows", sender, receiver)
}
