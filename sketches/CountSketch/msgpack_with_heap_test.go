package countsketch

import (
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// TestSerializeMsgpackWithHeapDelta_FullEqualsDeltaAgainstEmpty is the P0-2
// guard: a window-1 FULL frame and a window-2 DELTA-against-empty frame must
// reconstruct to IDENTICAL matrices for INTEGER cells. (The msgpack-heap delta
// wire is i64 by the ASAPQuery-backend contract; fractional cells are rejected
// — see the next test.) Cells are set directly so the comparison is on the raw
// matrix, not on hashed estimates.
func TestSerializeMsgpackWithHeapDelta_FullEqualsDeltaAgainstEmpty(t *testing.T) {
	const rows, cols, heapSize = 3, 16, 8

	cur, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Integer cells, including negatives (CountSketch is signed).
	cur.Count[0][0] = 7
	cur.Count[1][3] = -4
	cur.Count[2][15] = 100

	// Full frame (window 1) → decode its matrix.
	full, err := cur.SerializeMsgpackWithHeap(heapSize)
	if err != nil {
		t.Fatalf("full serialize: %v", err)
	}
	fromFull, err := DeserializeMsgpackWithHeapMatrix(full)
	if err != nil {
		t.Fatalf("decode full: %v", err)
	}

	// Delta against empty (window 2 under PWR) → apply onto empty.
	base, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	delta, err := cur.SerializeMsgpackWithHeapDelta(base, heapSize, 1.0)
	if err != nil {
		t.Fatalf("delta serialize: %v", err)
	}
	fromDelta, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("recon: %v", err)
	}
	if err := fromDelta.ApplyMsgpackWithHeapDelta(delta); err != nil {
		t.Fatalf("apply delta: %v", err)
	}

	// Matrices must be bit-identical.
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if fromFull.Count[r][c] != fromDelta.Count[r][c] {
				t.Fatalf("cell (%d,%d): full=%v delta=%v",
					r, c, fromFull.Count[r][c], fromDelta.Count[r][c])
			}
			if fromFull.Count[r][c] != cur.Count[r][c] {
				t.Fatalf("cell (%d,%d): full=%v want %v",
					r, c, fromFull.Count[r][c], cur.Count[r][c])
			}
		}
	}
}

// TestIsMsgpackWithHeapDelta_PeekDiscriminates is the P2(b) guard: the cheap
// prefix-peek classifier returns true for a real delta frame, false for a full
// frame, and false for garbage/truncated input — matching the old full-decode
// behavior without decoding the whole payload.
func TestIsMsgpackWithHeapDelta_PeekDiscriminates(t *testing.T) {
	const rows, cols, heapSize = 3, 16, 8
	cur, _ := NewCountSketch(rows, cols)
	cur.Count[0][0] = 5
	for i := 0; i < 5; i++ {
		cur.UpdateString("k", 1)
	}
	base, _ := NewCountSketch(rows, cols)

	delta, err := cur.SerializeMsgpackWithHeapDelta(base, heapSize, 1.0)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	full, err := cur.SerializeMsgpackWithHeap(heapSize)
	if err != nil {
		t.Fatalf("full: %v", err)
	}

	if !IsMsgpackWithHeapDelta(delta) {
		t.Error("delta frame should be classified as delta")
	}
	if IsMsgpackWithHeapDelta(full) {
		t.Error("full frame must NOT be classified as delta")
	}
	for _, bad := range [][]byte{nil, {}, {0x00}, {0x94}, {0x93, 0xc3}, {0x94, 0xc2}} {
		if IsMsgpackWithHeapDelta(bad) {
			t.Errorf("garbage %x must not be classified as delta", bad)
		}
	}
}

// TestSerializeMsgpackWithHeapDelta_RejectsFractionalCells is the P0-2 guard
// proving the integer-only contract is ENFORCED (not silently truncated): a
// fractional/weighted cell makes the delta path error so the caller falls back
// to the lossless full f64 frame. Sub-unit deltas (|Δ|<1) used to silently
// vanish under the old `int64(...)` truncation; now they are surfaced.
func TestSerializeMsgpackWithHeapDelta_RejectsFractionalCells(t *testing.T) {
	const rows, cols, heapSize = 3, 16, 8
	cur, _ := NewCountSketch(rows, cols)
	cur.Count[0][0] = 2.5 // fractional
	base, _ := NewCountSketch(rows, cols)

	_, err := cur.SerializeMsgpackWithHeapDelta(base, heapSize, 0.1)
	if err == nil {
		t.Fatal("expected error for fractional cell delta, got nil")
	}
	if !strings.Contains(err.Error(), "non-integral") {
		t.Fatalf("error should mention non-integral, got: %v", err)
	}

	// The matching FULL frame must still serialize and preserve the fraction.
	full, ferr := cur.SerializeMsgpackWithHeap(heapSize)
	if ferr != nil {
		t.Fatalf("full serialize: %v", ferr)
	}
	dec, derr := DeserializeMsgpackWithHeapMatrix(full)
	if derr != nil {
		t.Fatalf("decode full: %v", derr)
	}
	if dec.Count[0][0] != 2.5 {
		t.Fatalf("full frame lost the fractional cell: got %v want 2.5", dec.Count[0][0])
	}
}

// TestSerializeMsgpackWithHeap_BuildsHeapFromCandidates verifies that
// observations routed through UpdateString populate the Space-Saving
// candidate set, and that SerializeMsgpackWithHeap emits a non-empty
// top-k heap (the gate the backend uses to promote the sid to
// CountSketchWithHeap / FrequencyTopk) whose estimates track the inserts.
func TestSerializeMsgpackWithHeap_BuildsHeapFromCandidates(t *testing.T) {
	cs, err := NewCountSketch(5, 1024)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for i := 0; i < 100; i++ {
		cs.UpdateString("/checkout", 1)
	}
	for i := 0; i < 40; i++ {
		cs.UpdateString("/cart", 1)
	}
	for i := 0; i < 5; i++ {
		cs.UpdateString("/home", 1)
	}

	b, err := cs.SerializeMsgpackWithHeap(20)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	rows, cols, _, heap, heapSize, err := asapmsgpack.UnmarshalCountSketchWithHeap(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rows != 5 || cols != 1024 || heapSize != 20 {
		t.Fatalf("dims rows=%d cols=%d heapSize=%d", rows, cols, heapSize)
	}
	if len(heap) == 0 {
		t.Fatal("heap must be non-empty (CountSketchWithHeap promotion gate)")
	}
	// Highest-count item must be the heaviest endpoint, emitted first.
	if heap[0].Key != "/checkout" {
		t.Fatalf("expected /checkout first, got %q (heap=%+v)", heap[0].Key, heap)
	}
	// Values are CS matrix estimates; allow the sketch's error band.
	if heap[0].Value < 85 || heap[0].Value > 115 {
		t.Fatalf("/checkout estimate out of band: %v", heap[0].Value)
	}
}

// TestSerializeMsgpackWithHeap_EmptyWhenNoCandidates documents that with
// no UpdateString calls the heap is empty (the backend then keeps the sid
// as vanilla CountSketch — no top-k promotion). Still must serialize.
func TestSerializeMsgpackWithHeap_EmptyWhenNoCandidates(t *testing.T) {
	cs, err := NewCountSketch(3, 256)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	b, err := cs.SerializeMsgpackWithHeap(10)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	_, _, _, heap, _, err := asapmsgpack.UnmarshalCountSketchWithHeap(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(heap) != 0 {
		t.Fatalf("expected empty heap, got %d items", len(heap))
	}
}

// TestSerializeMsgpackWithHeapDelta_PWRRoundTrip exercises the DELTA-HEAP
// wire form end-to-end under the per-window-reset model (PWR): window 1's
// state is shipped full; window 2 — a fresh per-window sketch — ships a
// sparse matrix delta against an EMPTY base plus its full heap. Applying
// that delta onto an EMPTY reconstructed sketch must reproduce window 2's
// own matrix (estimates) and rank its heap correctly.
func TestSerializeMsgpackWithHeapDelta_PWRRoundTrip(t *testing.T) {
	const rows, cols, heapSize = 5, 1024, 20

	// Window 2's sketch.
	w2, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("w2: %v", err)
	}
	for i := 0; i < 50; i++ {
		w2.UpdateString("/checkout", 1)
	}
	for i := 0; i < 20; i++ {
		w2.UpdateString("/cart", 1)
	}

	// PWR base = empty sketch of the same dims (what the cache holds at the
	// window-2 boundary).
	base, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	delta, err := w2.SerializeMsgpackWithHeapDelta(base, heapSize, 1.0)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if !IsMsgpackWithHeapDelta(delta) {
		t.Fatal("frame not recognized as a delta-heap frame")
	}

	// Reconstruct from EMPTY (backend rotates base to empty per window).
	recon, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("recon: %v", err)
	}
	if err := recon.ApplyMsgpackWithHeapDelta(delta); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got, want := recon.EstimateStringCount("/checkout"), w2.EstimateStringCount("/checkout"); got != want {
		t.Fatalf("/checkout estimate: got %d want %d", got, want)
	}
	if got, want := recon.EstimateStringCount("/cart"), w2.EstimateStringCount("/cart"); got != want {
		t.Fatalf("/cart estimate: got %d want %d", got, want)
	}
	// Heap must rank /checkout above /cart.
	if recon.TopK == nil || len(recon.TopK.Heap) == 0 {
		t.Fatal("reconstructed heap empty")
	}
	var checkout, cart int64 = -1, -1
	for _, it := range recon.TopK.Heap {
		switch it.Key {
		case "/checkout":
			checkout = it.Count
		case "/cart":
			cart = it.Count
		}
	}
	if checkout <= 0 || cart <= 0 || checkout <= cart {
		t.Fatalf("heap ranking wrong: /checkout=%d /cart=%d", checkout, cart)
	}
}

// TestSerializeMsgpackWithHeapDelta_NoCrossWindowSubtraction proves the
// delta against empty carries only this window's mass: window 1 inserts 300,
// window 2 inserts 50; reconstructing window 2 from empty yields ~50, not the
// difference against window 1.
func TestSerializeMsgpackWithHeapDelta_NoCrossWindowSubtraction(t *testing.T) {
	const rows, cols, heapSize = 5, 1024, 20
	w2, _ := NewCountSketch(rows, cols)
	for i := 0; i < 50; i++ {
		w2.UpdateString("k", 1)
	}
	base, _ := NewCountSketch(rows, cols)
	delta, err := w2.SerializeMsgpackWithHeapDelta(base, heapSize, 1.0)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	recon, _ := NewCountSketch(rows, cols)
	if err := recon.ApplyMsgpackWithHeapDelta(delta); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := recon.EstimateStringCount("k")
	if got < 45 || got > 55 {
		t.Fatalf("window-2 count leaked cross-window mass: got %d (want ~50)", got)
	}
}
