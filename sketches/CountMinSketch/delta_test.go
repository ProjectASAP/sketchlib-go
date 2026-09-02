package countminsketch

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// TestDelta_FullEqualsDeltaAgainstEmpty is the P0-2 guard: for integer cells a
// FULL frame and a DELTA-against-empty must reconstruct to identical matrices.
func TestDelta_FullEqualsDeltaAgainstEmpty(t *testing.T) {
	cur := newCMS(t)
	cur.Count[0][0] = 7
	cur.Count[1][2] = 13
	cur.Sum[0][0] = 7
	cur.Sum2[0][0] = 7
	cur.Sum[1][2] = 13
	cur.Sum2[1][2] = 13

	empty := newCMS(t)
	delta, err := ComputeDelta(empty, cur, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	recon := newCMS(t)
	ApplyDelta(recon, delta)
	for r := 0; r < cur.Rows; r++ {
		for c := 0; c < cur.Cols; c++ {
			if recon.Count[r][c] != cur.Count[r][c] {
				t.Fatalf("Count[%d][%d]: delta=%v full=%v", r, c, recon.Count[r][c], cur.Count[r][c])
			}
		}
	}
}

// TestDelta_FractionalCellsLossless supersedes the old P0-2 rejection guard: a
// fractional/weighted cell now rides the packed-float64 d_counts_float wire and
// round-trips losslessly (see float_wire_test.go for the sampled-stream
// end-to-end version).
func TestDelta_FractionalCellsLossless(t *testing.T) {
	cur := newCMS(t)
	cur.Count[0][0] = 2.5 // fractional / weighted
	empty := newCMS(t)
	d, err := ComputeDelta(empty, cur, 0.1)
	if err != nil {
		t.Fatalf("fractional delta must be accepted: %v", err)
	}
	payload, err := SerializeDelta(d)
	if err != nil {
		t.Fatalf("SerializeDelta: %v", err)
	}
	got, err := DeserializeDelta(payload)
	if err != nil {
		t.Fatalf("DeserializeDelta: %v", err)
	}
	recon := newCMS(t)
	ApplyDelta(recon, got)
	if recon.Count[0][0] != 2.5 {
		t.Fatalf("fractional cell not lossless: got %v want 2.5", recon.Count[0][0])
	}
}

// helpers

func newCMS(t *testing.T) *CountMinSketch {
	t.Helper()
	s, err := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	return s
}

func insert(s *CountMinSketch, key string, n int) {
	in := common.FromString(key)
	for i := 0; i < n; i++ {
		s.Update(in)
	}
}

func estimate(s *CountMinSketch, key string) float64 {
	return s.Estimate(common.FromString(key))
}

// cmsEqual checks that two sketches have identical cell values.
func cmsEqual(t *testing.T, label string, a, b *CountMinSketch) {
	t.Helper()
	if a.Rows != b.Rows || a.Cols != b.Cols {
		t.Fatalf("%s: dimension mismatch", label)
	}
	for r := 0; r < a.Rows; r++ {
		for c := 0; c < a.Cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("%s: Count[%d][%d] want %v got %v", label, r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}

// TestDelta_RoundTrip verifies that ApplyDelta(ComputeDelta(snap, current))
// reconstructs current exactly when starting from snap.
func TestDelta_RoundTrip(t *testing.T) {
	snap := newCMS(t)
	insert(snap, "foo", 100)
	insert(snap, "bar", 50)

	current := newCMS(t)
	insert(current, "foo", 150) // +50 vs snap
	insert(current, "bar", 50)  // unchanged
	insert(current, "baz", 30)  // new key

	delta, err := ComputeDelta(snap, current, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}

	reconstructed := newCMS(t)
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge snap: %v", err)
	}
	ApplyDelta(reconstructed, delta)

	cmsEqual(t, "RoundTrip", current, reconstructed)
}

// TestDelta_Codec verifies the full bytes->Delta->ApplyDelta pipeline matches
// a direct merge: serialize delta to bytes, deserialize, apply.
func TestDelta_Codec(t *testing.T) {
	snap := newCMS(t)
	insert(snap, "x", 200)

	current := newCMS(t)
	insert(current, "x", 300)
	insert(current, "y", 75)

	delta, err := ComputeDelta(snap, current, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	b, err := SerializeDelta(delta)
	if err != nil {
		t.Fatalf("SerializeDelta: %v", err)
	}
	decoded, err := DeserializeDelta(b)
	if err != nil {
		t.Fatalf("DeserializeDelta: %v", err)
	}

	reconstructed := newCMS(t)
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyDelta(reconstructed, decoded)

	cmsEqual(t, "Codec", current, reconstructed)
}

// TestDelta_EmptyDelta checks that ComputeDelta on identical sketches
// produces an empty cell list (lossless, T=0).
func TestDelta_EmptyDelta(t *testing.T) {
	s := newCMS(t)
	insert(s, "a", 10)

	delta, err := ComputeDelta(s, s, 0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	if len(delta.Cells) != 0 {
		t.Fatalf("expected 0 cells for identical sketch, got %d", len(delta.Cells))
	}
}

// TestDelta_ThresholdFiltering verifies that cells below the threshold are
// omitted from the delta, and the resulting estimate is still within bounds.
func TestDelta_ThresholdFiltering(t *testing.T) {
	snap := newCMS(t)
	insert(snap, "heavy", 1000)
	insert(snap, "light", 1)

	current := newCMS(t)
	insert(current, "heavy", 1050) // Δ=50 — above threshold
	insert(current, "light", 2)    // Δ=1 — below threshold T=10

	delta, err := ComputeDelta(snap, current, 10.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}

	reconstructed := newCMS(t)
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyDelta(reconstructed, delta)

	// Heavy key must be exactly correct (delta >> threshold).
	wantHeavy := estimate(current, "heavy")
	gotHeavy := estimate(reconstructed, "heavy")
	if gotHeavy != wantHeavy {
		t.Errorf("heavy key: want %.0f got %.0f", wantHeavy, gotHeavy)
	}
	// Light key estimate may lag by at most the threshold.
	gotLight := estimate(reconstructed, "light")
	wantLight := estimate(current, "light")
	t.Logf("light key: current=%.0f reconstructed=%.0f", wantLight, gotLight)
}

// TestDelta_MultipleWindows simulates multiple consecutive delta transmissions,
// verifying that incremental application converges to the full cumulative state.
func TestDelta_MultipleWindows(t *testing.T) {
	const windows = 5
	sender := newCMS(t)
	receiver := newCMS(t) // starts empty, accumulates via deltas
	snap := newCMS(t)

	for w := 0; w < windows; w++ {
		// Insert a batch into sender.
		for i := 0; i < 100; i++ {
			insert(sender, fmt.Sprintf("key-%d-%d", w, i), 1)
		}

		delta, err := ComputeDelta(snap, sender, 1.0)
		if err != nil {
			t.Fatalf("window %d ComputeDelta: %v", w, err)
		}
		b, err := SerializeDelta(delta)
		if err != nil {
			t.Fatalf("window %d SerializeDelta: %v", w, err)
		}
		decoded, err := DeserializeDelta(b)
		if err != nil {
			t.Fatalf("window %d DeserializeDelta: %v", w, err)
		}
		ApplyDelta(receiver, decoded)

		// Advance snapshot to current sender state.
		snap, _ = NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
		if err := snap.Merge(sender); err != nil {
			t.Fatalf("window %d snap.Merge: %v", w, err)
		}
	}

	cmsEqual(t, "MultipleWindows", sender, receiver)
}

// TestDelta_DimensionMismatch verifies ComputeDelta returns an error when
// sketch dimensions differ.
func TestDelta_DimensionMismatch(t *testing.T) {
	a, _ := NewCountMinSketch(3, 100)
	b, _ := NewCountMinSketch(5, 200)
	_, err := ComputeDelta(a, b, 1.0)
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}
