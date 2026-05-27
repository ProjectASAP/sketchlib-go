package asapmsgpack

import (
	"encoding/hex"
	"testing"
)

// TestMarshalCountSketchWithHeap_RustGoldenStructure pins the byte
// structure produced for a known input against the layout the Rust
// `CountMinSketchWithHeap::from_msgpack` reads (rmp_serde compact). The
// expected bytes were captured by serializing the equivalent Rust
// `CountMinSketchWithHeap` (2x4 matrix, heap {/checkout:100, /cart:40},
// heap_size 3) and re-ordering the heap items to our deterministic
// highest-count-first emission. The backend re-sorts the heap by value
// after decode, so item order is not part of the correctness contract;
// this golden guards the framing (array headers, ints, floats, strings).
func TestMarshalCountSketchWithHeap_RustGoldenStructure(t *testing.T) {
	matrix := [][]float64{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
	}
	heap := []HeapItem{
		{Key: "/checkout", Value: 100},
		{Key: "/cart", Value: 40},
	}
	got, err := MarshalCountSketchWithHeap(2, 4, matrix, heap, 3)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// rmp_serde compact layout: outer array(3) = [ inner array(3), heap array(2), heap_size ].
	want := "93939294cb3ff0000000000000cb4000000000000000cb4008000000000000cb4010" +
		"00000000000094cb4014000000000000cb4018000000000000cb401c000000000000cb" +
		"402000000000000002049292a92f636865636b6f7574cb405900000000000092a52f63" +
		"617274cb404400000000000003"
	if h := hex.EncodeToString(got); h != want {
		t.Fatalf("byte layout mismatch:\n got=%s\nwant=%s", h, want)
	}
}

// TestCountSketchWithHeap_RoundTrip exercises the Go-side inverse and
// confirms every field survives.
func TestCountSketchWithHeap_RoundTrip(t *testing.T) {
	matrix := [][]float64{
		{-3, 7, 0, 2},
		{4, -1, 9, 5},
	}
	heap := []HeapItem{
		{Key: "alpha", Value: 99},
		{Key: "beta", Value: 12},
		{Key: "gamma", Value: 3},
	}
	b, err := MarshalCountSketchWithHeap(2, 4, matrix, heap, 5)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rows, cols, gotMatrix, gotHeap, heapSize, err := UnmarshalCountSketchWithHeap(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rows != 2 || cols != 4 || heapSize != 5 {
		t.Fatalf("dims: rows=%d cols=%d heapSize=%d", rows, cols, heapSize)
	}
	for r := range matrix {
		for c := range matrix[r] {
			if gotMatrix[r][c] != matrix[r][c] {
				t.Fatalf("matrix[%d][%d]: got %v want %v", r, c, gotMatrix[r][c], matrix[r][c])
			}
		}
	}
	if len(gotHeap) != len(heap) {
		t.Fatalf("heap len: got %d want %d", len(gotHeap), len(heap))
	}
	for i := range heap {
		if gotHeap[i].Key != heap[i].Key || gotHeap[i].Value != heap[i].Value {
			t.Fatalf("heap[%d]: got %+v want %+v", i, gotHeap[i], heap[i])
		}
	}
}

// TestMarshalCountSketchWithHeap_RejectsNonRectangular guards the shape check.
func TestMarshalCountSketchWithHeap_RejectsNonRectangular(t *testing.T) {
	matrix := [][]float64{{1, 2, 3}, {4, 5}}
	if _, err := MarshalCountSketchWithHeap(2, 3, matrix, nil, 1); err == nil {
		t.Fatal("expected error for ragged matrix")
	}
}

// TestMarshalCountSketchWithHeapDelta_RoundTrip exercises the DELTA-HEAP
// wire form's Go-side inverse and confirms every field survives, including
// signed cell deltas.
func TestMarshalCountSketchWithHeapDelta_RoundTrip(t *testing.T) {
	cells := []HeapCellDelta{
		{Row: 0, Col: 3, DCount: 7},
		{Row: 1, Col: 0, DCount: -4},
		{Row: 4, Col: 1023, DCount: 1000000},
	}
	heap := []HeapItem{
		{Key: "/checkout", Value: 99},
		{Key: "/cart", Value: 12},
	}
	b, err := MarshalCountSketchWithHeapDelta(5, 1024, cells, heap, 20)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rows, cols, gotCells, gotHeap, heapSize, err := UnmarshalCountSketchWithHeapDelta(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rows != 5 || cols != 1024 || heapSize != 20 {
		t.Fatalf("dims: rows=%d cols=%d heapSize=%d", rows, cols, heapSize)
	}
	if len(gotCells) != len(cells) {
		t.Fatalf("cells len: got %d want %d", len(gotCells), len(cells))
	}
	for i := range cells {
		if gotCells[i] != cells[i] {
			t.Fatalf("cell[%d]: got %+v want %+v", i, gotCells[i], cells[i])
		}
	}
	if len(gotHeap) != len(heap) {
		t.Fatalf("heap len: got %d want %d", len(gotHeap), len(heap))
	}
	for i := range heap {
		if gotHeap[i] != heap[i] {
			t.Fatalf("heap[%d]: got %+v want %+v", i, gotHeap[i], heap[i])
		}
	}
}

// TestDeltaFrame_DistinctFromFullFrame guards the framing invariant: a delta
// frame (4-element array, is_delta marker) is NOT decodable as a full frame,
// and a full frame is NOT decodable as a delta frame. This is what lets the
// decoder route the two unambiguously.
func TestDeltaFrame_DistinctFromFullFrame(t *testing.T) {
	full, err := MarshalCountSketchWithHeap(2, 2, [][]float64{{1, 2}, {3, 4}},
		[]HeapItem{{Key: "a", Value: 9}}, 5)
	if err != nil {
		t.Fatalf("full marshal: %v", err)
	}
	delta, err := MarshalCountSketchWithHeapDelta(2, 2,
		[]HeapCellDelta{{Row: 0, Col: 0, DCount: 1}},
		[]HeapItem{{Key: "a", Value: 9}}, 5)
	if err != nil {
		t.Fatalf("delta marshal: %v", err)
	}
	if _, _, _, _, _, err := UnmarshalCountSketchWithHeapDelta(full); err == nil {
		t.Fatal("full frame wrongly decoded as a delta frame")
	}
	if _, _, _, _, _, err := UnmarshalCountSketchWithHeap(delta); err == nil {
		t.Fatal("delta frame wrongly decoded as a full frame")
	}
}
