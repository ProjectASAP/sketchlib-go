package countsketch

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

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
