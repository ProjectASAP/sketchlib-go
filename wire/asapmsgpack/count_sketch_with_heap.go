package asapmsgpack

import "fmt"

// HeapItem is one (key, count) entry of a CountSketch / CountMinSketch
// top-k heap on the wire. The count is carried as float64 to match the
// Rust `CmsHeapItem { key: String, value: f64 }` field types.
type HeapItem struct {
	Key   string
	Value float64
}

// MarshalCountSketchWithHeap emits the MessagePack payload that the Rust
// consumer's `asap_sketchlib::CountMinSketchWithHeap::from_msgpack`
// accepts. ASAPQuery-backend detects a heap-bearing CountSketch on the
// modified-OTLP ingest path by attempting exactly this decode and
// promoting the sid to `CountSketchWithHeap` when the bytes round-trip
// AND the heap is non-empty (see ingest/otel.rs::sketch_kind_handle_for,
// the `SketchKind::CountSketch` + `ENCODING_MSGPACK` branch). Both
// heap-bearing frequency variants (CMS-with-heap and CountSketch-with-
// heap) share this single outer wire shape — the heap is the
// distinguishing payload, not a distinct proto/struct.
//
// Wire format — rmp_serde compact mode on the Rust
// `CountMinSketchWithHeapWire` struct, which serializes as a 3-element
// fixed array in field-declaration order:
//
//	[
//	  // CountMinSketchInnerWire (also a 3-element array, declaration order):
//	  [ matrix:[][]float64, rows:uint, cols:uint ],
//	  // topk_heap: Vec<CmsHeapItem>, each item a 2-element array:
//	  [ [key:string, value:float64], ... ],
//	  heap_size:uint,
//	]
//
// rmp_serde encodes structs as positional arrays (the field names
// `row_num`/`col_num`/`sketch`/`topk_heap`/`heap_size` are NOT in the
// bytes), strings as fixstr/str8/…, and f64 as 0xcb — matching the
// primitives in encoder.go. The backend's `from_msgpack` re-sorts the
// heap by value descending after decode, so the order this function
// emits heap items in does not affect decode correctness.
//
// `matrix` MUST be rectangular shape [rows][cols]; CountSketch cells are
// signed (median-of-rows), and the backend rounds them to i64 when
// rebuilding its CMS-shaped storage — it answers `topk` from the heap
// (keys + counts) and point-frequency from the rebuilt matrix.
func MarshalCountSketchWithHeap(
	rows, cols uint64,
	matrix [][]float64,
	heap []HeapItem,
	heapSize uint64,
) ([]byte, error) {
	if uint64(len(matrix)) != rows {
		return nil, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeap matrix has %d rows, expected %d",
			len(matrix), rows)
	}
	for r, row := range matrix {
		if uint64(len(row)) != cols {
			return nil, fmt.Errorf(
				"asapmsgpack: CountSketchWithHeap row %d has %d cols, expected %d",
				r, len(row), cols)
		}
	}
	e := newEncoder()
	// Outer wire: 3-element array.
	e.writeArrayLen(3)
	// Field 0: inner CountMinSketchInnerWire = [matrix, rows, cols].
	e.writeArrayLen(3)
	e.writeFloat64Matrix(matrix)
	e.writeUint(rows)
	e.writeUint(cols)
	// Field 1: topk_heap = array of [key, value] pairs.
	e.writeArrayLen(len(heap))
	for _, it := range heap {
		e.writeArrayLen(2)
		e.writeString(it.Key)
		e.writeFloat64(it.Value)
	}
	// Field 2: heap_size.
	e.writeUint(heapSize)
	return e.bytes(), nil
}

// HeapCellDelta is one signed (row, col, Δcount) entry of a sparse
// CountSketch-with-heap matrix delta on the wire. The delta count is an
// integer (CountSketch cells are stored as signed i64 on the backend); it
// is carried as a msgpack int so the backend reads it via the same
// positional-array contract as the rest of this format.
type HeapCellDelta struct {
	Row, Col uint32
	DCount   int64
}

// MarshalCountSketchWithHeapDelta emits the DELTA-HEAP wire form: a sparse
// signed matrix delta plus the FULL (small, ≤heap_size) top-k heap. It is
// the bandwidth-saving companion to MarshalCountSketchWithHeap (the
// full-frame form) — the producer ships a full frame for window 1 and a
// delta-heap frame for every later window (per-window-reset / PWR model,
// docs/delta-baseline-contract.md §3): each delta is that window's own
// matrix encoded sparsely against an EMPTY base, so the backend (which
// rotates its per-series base to empty at each window boundary) applies the
// delta onto zeros and reconstructs the window's own state.
//
// The frame is a DISTINCT shape from the full frame so a decoder can never
// confuse the two: the full frame is a 3-element array, the delta frame a
// 4-element array whose FIRST element is the `is_delta` boolean marker:
//
//	[
//	  is_delta: bool (always true here),
//	  // sparse matrix delta = [rows, cols, cells]:
//	  [ rows:uint, cols:uint, [ [row:uint, col:uint, dcount:int], ... ] ],
//	  // topk_heap: FULL heap, array of [key, value] pairs:
//	  [ [key:string, value:float64], ... ],
//	  heap_size:uint,
//	]
//
// On the wire the OTLP `CountSketchDataPoint.encoding` is
// `COUNT_SKETCH_ENCODING_MSGPACK_DELTA = 4`, which is what the backend
// dispatches on; the in-frame `is_delta` bool is a defensive secondary
// signal. The matrix-delta cell shape mirrors the plain-CountSketch sparse
// delta (signed per-(r,c) Δcount); the backend applies it additively onto
// its stored matrix and replaces the stored heap with this frame's heap.
func MarshalCountSketchWithHeapDelta(
	rows, cols uint64,
	cells []HeapCellDelta,
	heap []HeapItem,
	heapSize uint64,
) ([]byte, error) {
	e := newEncoder()
	// Outer wire: 4-element array (distinct from the 3-element full frame).
	e.writeArrayLen(4)
	// Field 0: is_delta marker (msgpack true = 0xc3).
	e.writeBool(true)
	// Field 1: sparse matrix delta = [rows, cols, cells].
	e.writeArrayLen(3)
	e.writeUint(rows)
	e.writeUint(cols)
	e.writeArrayLen(len(cells))
	for _, c := range cells {
		e.writeArrayLen(3)
		e.writeUint(uint64(c.Row))
		e.writeUint(uint64(c.Col))
		e.writeInt(c.DCount)
	}
	// Field 2: topk_heap = array of [key, value] pairs (the FULL heap).
	e.writeArrayLen(len(heap))
	for _, it := range heap {
		e.writeArrayLen(2)
		e.writeString(it.Key)
		e.writeFloat64(it.Value)
	}
	// Field 3: heap_size.
	e.writeUint(heapSize)
	return e.bytes(), nil
}

// UnmarshalCountSketchWithHeapDelta is the inverse of
// MarshalCountSketchWithHeapDelta, for Go-side round-trip tests and the
// cross-language byte-parity check. Production decode happens on the Rust
// side in data_plane (rmp_serde directly), NOT in asap_sketchlib.
func UnmarshalCountSketchWithHeapDelta(buf []byte) (
	rows, cols uint64, cells []HeapCellDelta, heap []HeapItem, heapSize uint64, err error,
) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if n != 4 {
		return 0, 0, nil, nil, 0, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeapDelta expected 4-element outer array, got %d", n)
	}
	isDelta, err := d.readBool()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if !isDelta {
		return 0, 0, nil, nil, 0, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeapDelta is_delta marker is false")
	}
	// Sparse matrix delta array = [rows, cols, cells].
	md, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if md != 3 {
		return 0, 0, nil, nil, 0, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeapDelta matrix-delta expected 3-element array, got %d", md)
	}
	rows, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	cols, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	cn, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	cells = make([]HeapCellDelta, 0, cn)
	for i := 0; i < cn; i++ {
		ce, cerr := d.readArrayLen()
		if cerr != nil {
			return 0, 0, nil, nil, 0, cerr
		}
		if ce != 3 {
			return 0, 0, nil, nil, 0, fmt.Errorf(
				"asapmsgpack: CountSketchWithHeapDelta cell expected 3-element array, got %d", ce)
		}
		r, rerr := d.readUint()
		if rerr != nil {
			return 0, 0, nil, nil, 0, rerr
		}
		col, colerr := d.readUint()
		if colerr != nil {
			return 0, 0, nil, nil, 0, colerr
		}
		dc, dcerr := d.readInt()
		if dcerr != nil {
			return 0, 0, nil, nil, 0, dcerr
		}
		cells = append(cells, HeapCellDelta{Row: uint32(r), Col: uint32(col), DCount: dc})
	}
	// topk_heap.
	hn, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	heap = make([]HeapItem, 0, hn)
	for i := 0; i < hn; i++ {
		pair, perr := d.readArrayLen()
		if perr != nil {
			return 0, 0, nil, nil, 0, perr
		}
		if pair != 2 {
			return 0, 0, nil, nil, 0, fmt.Errorf(
				"asapmsgpack: CountSketchWithHeapDelta heap item expected 2-element array, got %d", pair)
		}
		key, kerr := d.readString()
		if kerr != nil {
			return 0, 0, nil, nil, 0, kerr
		}
		val, verr := d.readFloat64()
		if verr != nil {
			return 0, 0, nil, nil, 0, verr
		}
		heap = append(heap, HeapItem{Key: key, Value: val})
	}
	heapSize, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if err := d.done(); err != nil {
		return 0, 0, nil, nil, 0, err
	}
	return rows, cols, cells, heap, heapSize, nil
}

// UnmarshalCountSketchWithHeap is the inverse of MarshalCountSketchWithHeap,
// for Go-side round-trip tests. Production decode happens on the Rust side
// via CountMinSketchWithHeap::from_msgpack.
func UnmarshalCountSketchWithHeap(buf []byte) (
	rows, cols uint64, matrix [][]float64, heap []HeapItem, heapSize uint64, err error,
) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if n != 3 {
		return 0, 0, nil, nil, 0, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeap expected 3-element outer array, got %d", n)
	}
	// Inner wire array.
	inner, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if inner != 3 {
		return 0, 0, nil, nil, 0, fmt.Errorf(
			"asapmsgpack: CountSketchWithHeap inner expected 3-element array, got %d", inner)
	}
	matrix, err = d.readFloat64Matrix()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	rows, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	cols, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	// topk_heap.
	hn, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	heap = make([]HeapItem, 0, hn)
	for i := 0; i < hn; i++ {
		pair, perr := d.readArrayLen()
		if perr != nil {
			return 0, 0, nil, nil, 0, perr
		}
		if pair != 2 {
			return 0, 0, nil, nil, 0, fmt.Errorf(
				"asapmsgpack: CountSketchWithHeap heap item expected 2-element array, got %d", pair)
		}
		key, kerr := d.readString()
		if kerr != nil {
			return 0, 0, nil, nil, 0, kerr
		}
		val, verr := d.readFloat64()
		if verr != nil {
			return 0, 0, nil, nil, 0, verr
		}
		heap = append(heap, HeapItem{Key: key, Value: val})
	}
	heapSize, err = d.readUint()
	if err != nil {
		return 0, 0, nil, nil, 0, err
	}
	if err := d.done(); err != nil {
		return 0, 0, nil, nil, 0, err
	}
	return rows, cols, matrix, heap, heapSize, nil
}
