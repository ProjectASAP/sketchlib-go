package countsketch

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// TestCSDelta_FullEqualsDeltaAgainstEmpty is the P0-2 guard: for integer cells
// a FULL snapshot and a DELTA-against-empty reconstruct to identical matrices.
// CountSketch cells are signed, so include a negative.
func TestCSDelta_FullEqualsDeltaAgainstEmpty(t *testing.T) {
	cur := newCS(t)
	cur.Count[0][0] = 9
	cur.Count[2][5] = -4
	cur.Count[4][100] = 250

	empty := newCS(t)
	delta, err := ComputeDelta(empty, cur, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	recon := newCS(t)
	ApplyDelta(recon, delta)
	for r := 0; r < cur.Rows; r++ {
		for c := 0; c < cur.Cols; c++ {
			if recon.Count[r][c] != cur.Count[r][c] {
				t.Fatalf("Count[%d][%d]: delta=%v full=%v", r, c, recon.Count[r][c], cur.Count[r][c])
			}
		}
	}
}

// TestCSDelta_FractionalCellsLossless supersedes the old P0-2 rejection guard:
// a fractional/weighted cell now rides the packed-float64 d_counts_float wire
// and round-trips losslessly (see float_wire_test.go for the sampled-stream
// end-to-end version).
func TestCSDelta_FractionalCellsLossless(t *testing.T) {
	cur := newCS(t)
	cur.Count[0][0] = -2.25 // fractional, signed
	empty := newCS(t)
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
	recon := newCS(t)
	ApplyDelta(recon, got)
	if recon.Count[0][0] != -2.25 {
		t.Fatalf("fractional cell not lossless: got %v want -2.25", recon.Count[0][0])
	}
}

// TestCSDelta_PerCellThreshold: a cell is included iff its delta meets ITS OWN
// threshold — a big cell under a tight threshold is sent; an equal cell under a
// loose threshold is dropped. Reconstruction applies only the included cells.
func TestCSDelta_PerCellThreshold(t *testing.T) {
	cur := newCS(t)
	cur.Count[0][0] = 50 // tight threshold below 50 → included
	cur.Count[1][1] = 50 // loose threshold above 50 → excluded
	empty := newCS(t)

	th := make([][]float64, cur.Rows)
	for r := range th {
		th[r] = make([]float64, cur.Cols)
		for c := range th[r] {
			th[r][c] = 1000 // default: loose (exclude everything)
		}
	}
	th[0][0] = 10 // tighten only (0,0)

	delta, err := ComputeDeltaPerCell(empty, cur, th)
	if err != nil {
		t.Fatalf("ComputeDeltaPerCell: %v", err)
	}
	if len(delta.Cells) != 1 || delta.Cells[0].Row != 0 || delta.Cells[0].Col != 0 {
		t.Fatalf("expected only cell (0,0), got %+v", delta.Cells)
	}
	recon := newCS(t)
	ApplyDelta(recon, delta)
	if recon.Count[0][0] != 50 {
		t.Errorf("(0,0) should apply: %v", recon.Count[0][0])
	}
	if recon.Count[1][1] != 0 {
		t.Errorf("(1,1) below its threshold must be dropped: %v", recon.Count[1][1])
	}
}

// A nil threshold matrix is lossless (threshold 0 everywhere).
func TestCSDelta_PerCellNilIsLossless(t *testing.T) {
	cur := newCS(t)
	cur.Count[0][0] = 7
	cur.Count[3][9] = -3
	empty := newCS(t)
	d, err := ComputeDeltaPerCell(empty, cur, nil)
	if err != nil {
		t.Fatalf("ComputeDeltaPerCell nil: %v", err)
	}
	if len(d.Cells) != 2 {
		t.Fatalf("nil thresholds → lossless (2 cells), got %d", len(d.Cells))
	}
}

func newCS(t *testing.T) *CountSketch {
	t.Helper()
	s, err := NewCountSketch(5, 2048)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	return s
}

func csInsert(s *CountSketch, key string, n int) {
	in := common.FromString(key)
	for i := 0; i < n; i++ {
		s.Update(in)
	}
}

func csEqual(t *testing.T, label string, a, b *CountSketch) {
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

// TestCSDelta_RoundTrip verifies that applying a computed delta to a clone of
// the snapshot reconstructs the current sketch exactly.
func TestCSDelta_RoundTrip(t *testing.T) {
	snap := newCS(t)
	csInsert(snap, "foo", 100)
	csInsert(snap, "bar", 50)

	current := newCS(t)
	csInsert(current, "foo", 150)
	csInsert(current, "bar", 50)
	csInsert(current, "baz", 30)

	delta, err := ComputeDelta(snap, current, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}

	reconstructed := newCS(t)
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyDelta(reconstructed, delta)

	csEqual(t, "RoundTrip", current, reconstructed)
}

// TestCSDelta_Codec verifies the full bytes->Delta->ApplyDelta pipeline.
func TestCSDelta_Codec(t *testing.T) {
	snap := newCS(t)
	csInsert(snap, "x", 200)

	current := newCS(t)
	csInsert(current, "x", 300)
	csInsert(current, "y", 75)

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

	reconstructed := newCS(t)
	if err := reconstructed.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ApplyDelta(reconstructed, decoded)

	csEqual(t, "Codec", current, reconstructed)
}

// TestCSDelta_EmptyDelta checks that identical sketches produce zero cells.
func TestCSDelta_EmptyDelta(t *testing.T) {
	s := newCS(t)
	csInsert(s, "a", 10)

	delta, err := ComputeDelta(s, s, 0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	if len(delta.Cells) != 0 {
		t.Fatalf("expected 0 cells for identical sketch, got %d", len(delta.Cells))
	}
}

// TestCSDelta_LinearityProperty verifies that delta transmission is equivalent
// to direct merge: merge(snap, delta) == current.
func TestCSDelta_LinearityProperty(t *testing.T) {
	// Build snap and current independently, then verify via direct merge too.
	snap := newCS(t)
	csInsert(snap, "alpha", 500)
	csInsert(snap, "beta", 200)

	increment := newCS(t)
	csInsert(increment, "alpha", 100)
	csInsert(increment, "gamma", 300)

	current := newCS(t)
	if err := current.Merge(snap); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := current.Merge(increment); err != nil {
		t.Fatalf("Merge increment: %v", err)
	}

	delta, err := ComputeDelta(snap, current, 1.0)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}
	b, _ := SerializeDelta(delta)
	decoded, _ := DeserializeDelta(b)

	recv := newCS(t)
	if err := recv.Merge(snap); err != nil {
		t.Fatalf("Merge recv: %v", err)
	}
	ApplyDelta(recv, decoded)

	csEqual(t, "LinearityProperty", current, recv)
}

// TestCSDelta_MultipleWindows simulates consecutive delta transmissions.
func TestCSDelta_MultipleWindows(t *testing.T) {
	sender := newCS(t)
	receiver := newCS(t)
	snap := newCS(t)

	for w := 0; w < 5; w++ {
		for i := 0; i < 50; i++ {
			csInsert(sender, fmt.Sprintf("key-%d-%d", w, i), 1)
		}
		delta, err := ComputeDelta(snap, sender, 1.0)
		if err != nil {
			t.Fatalf("window %d: %v", w, err)
		}
		b, _ := SerializeDelta(delta)
		decoded, _ := DeserializeDelta(b)
		ApplyDelta(receiver, decoded)

		snap = newCS(t)
		if err := snap.Merge(sender); err != nil {
			t.Fatalf("snap.Merge: %v", err)
		}
	}

	csEqual(t, "MultipleWindows", sender, receiver)
}

// TestCSDelta_DimensionMismatch verifies error on mismatched dimensions.
func TestCSDelta_DimensionMismatch(t *testing.T) {
	a, _ := NewCountSketch(3, 64)
	b, _ := NewCountSketch(5, 128)
	_, err := ComputeDelta(a, b, 1.0)
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}
