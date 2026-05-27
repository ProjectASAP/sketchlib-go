package countsketch

import (
	"fmt"
	"math"
	"sort"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// integralCellDelta converts a float64 cell delta to its exact int64 value,
// returning ok=false when the delta is fractional. The msgpack-heap DELTA
// wire carries cells as i64 (cross-repo contract with the ASAPQuery-backend's
// `cells: Vec<(u32,u32,i64)>` decode), while the FULL frame's matrix is f64.
// A fractional/weighted cell therefore cannot be represented losslessly on the
// delta wire, and truncating it would make a window-1 full frame and a
// window-2 delta-against-empty reconstruct to DIFFERENT matrices (and would
// silently vanish |Δ|<1 cells). Returning ok=false lets the caller reject the
// delta and fall back to the lossless full f64 frame instead of truncating.
func integralCellDelta(df float64) (int64, bool) {
	if math.IsNaN(df) || math.IsInf(df, 0) {
		return 0, false
	}
	r := math.Round(df)
	if r != df { // fractional (or beyond f64 integer precision)
		return 0, false
	}
	if r > math.MaxInt64 || r < math.MinInt64 {
		return 0, false
	}
	return int64(r), true
}

// SerializeMsgpack emits the MessagePack wire format ASAPQuery-backend
// decodes when the modified-OTLP `CountSketchDataPoint.encoding` is
// `COUNT_SKETCH_ENCODING_MSGPACK = 3` (cross-language contract with
// `sketch_core::count_sketch::CountSketch::deserialize_msgpack`).
//
// Wire field order mirrors the Rust struct declaration order:
// `[row_num, col_num, matrix]`. This differs from the legacy
// CountMinSketch wire format (which places the matrix first) —
// intentional; both reproduce their respective Rust sides exactly.
func (s *CountSketch) SerializeMsgpack() ([]byte, error) {
	return asapmsgpack.MarshalCountSketch(
		uint64(s.Rows),
		uint64(s.Cols),
		s.Count,
	)
}

// SerializeMsgpackWithHeap emits the heap-bearing MessagePack wire
// format ASAPQuery-backend reads via
// `asap_sketchlib::CountMinSketchWithHeap::from_msgpack`. The backend
// promotes a `SketchKind::CountSketch` + `ENCODING_MSGPACK` data point to
// the `CountSketchWithHeap` sketch kind (capability `FrequencyTopk`) iff
// this decode succeeds AND the decoded heap is non-empty — that is how a
// `topk(metric)` query routes to this sid instead of returning
// "No result" (see ingest/otel.rs::sketch_kind_handle_for).
//
// The top-k heap is built from the producer's heavy-hitter candidate set
// (the internal Weighted Space-Saving tracker maintained by UpdateString),
// each candidate's count taken from the CountSketch matrix estimate. At
// most heapSize items are emitted, the highest-count ones; ties broken by
// key for deterministic output. heapSize is clamped to >=1.
//
// When the sketch has no candidates (no UpdateString calls, or SS absent)
// the heap is empty — and the backend then will NOT promote the sid to
// CountSketchWithHeap (it stays vanilla CountSketch / FrequencyEstimate).
// Callers that need top-k must ensure observations flowed through
// UpdateString (the keyed-observe path), not the hash-only Insert path.
func (s *CountSketch) SerializeMsgpackWithHeap(heapSize int) ([]byte, error) {
	if heapSize < 1 {
		heapSize = 1
	}
	heap := s.buildWireHeap(heapSize)
	return asapmsgpack.MarshalCountSketchWithHeap(
		uint64(s.Rows),
		uint64(s.Cols),
		s.Count,
		heap,
		uint64(heapSize),
	)
}

// SerializeMsgpackWithHeapDelta emits the DELTA-HEAP MessagePack wire form
// (encoding tag COUNT_SKETCH_ENCODING_MSGPACK_DELTA = 4): a SPARSE signed
// matrix delta of `s` against `base` plus the FULL (≤heapSize) top-k heap
// of `s`. It is the bandwidth-saving companion to SerializeMsgpackWithHeap
// (the full frame). Cells whose |Δ| < threshold are dropped from the delta.
//
// Per-window-reset model (PWR, docs/delta-baseline-contract.md §3): the
// producer ships a full frame for window 1 and a delta-heap frame for each
// later window, computed against an EMPTY base of the same dims (so each
// delta IS that window's own matrix encoded sparsely). The backend rotates
// its per-series base to empty at the window boundary and applies the delta
// additively, reconstructing the window's own state. The heap is small and
// non-additive, so it is always shipped in full.
//
// `base` MUST have the same (rows, cols) as `s`.
func (s *CountSketch) SerializeMsgpackWithHeapDelta(
	base *CountSketch, heapSize int, threshold float64,
) ([]byte, error) {
	if heapSize < 1 {
		heapSize = 1
	}
	if base == nil {
		return nil, fmt.Errorf("countsketch: SerializeMsgpackWithHeapDelta: nil base")
	}
	if base.Rows != s.Rows || base.Cols != s.Cols {
		return nil, fmt.Errorf(
			"countsketch: SerializeMsgpackWithHeapDelta dimension mismatch (%dx%d vs %dx%d)",
			base.Rows, base.Cols, s.Rows, s.Cols)
	}
	cells := make([]asapmsgpack.HeapCellDelta, 0, s.Rows*s.Cols/20)
	for r := 0; r < s.Rows; r++ {
		baseRow := base.Count[r]
		curRow := s.Count[r]
		for c := 0; c < s.Cols; c++ {
			df := curRow[c] - baseRow[c]
			// Threshold compared on the float magnitude (not a truncated int)
			// so a fractional threshold like 0.5 behaves as written and
			// sub-unit deltas are not silently mis-filtered.
			if df == 0 || math.Abs(df) < threshold {
				continue
			}
			dc, ok := integralCellDelta(df)
			if !ok {
				// Fractional/weighted cell: the i64 delta wire cannot carry it
				// losslessly. Reject so the caller emits the full f64 frame
				// (which DOES preserve fractional cells), keeping full ≡
				// delta-against-empty reconstruction exact.
				return nil, fmt.Errorf(
					"countsketch: SerializeMsgpackWithHeapDelta: cell (%d,%d) delta %v is "+
						"non-integral; the msgpack-heap delta wire is integer-only "+
						"(use the full frame for weighted/fractional sketches)", r, c, df)
			}
			cells = append(cells, asapmsgpack.HeapCellDelta{
				Row: uint32(r), Col: uint32(c), DCount: dc,
			})
		}
	}
	heap := s.buildWireHeap(heapSize)
	return asapmsgpack.MarshalCountSketchWithHeapDelta(
		uint64(s.Rows),
		uint64(s.Cols),
		cells,
		heap,
		uint64(heapSize),
	)
}

// buildWireHeap returns up to heapSize (key, estimatedCount) pairs for the
// sketch's heavy-hitter candidates, highest count first. Counts come from
// the CS matrix estimate for each candidate key; non-positive estimates
// are dropped (the backend's heap-rebuild also drops count<=0 items).
func (s *CountSketch) buildWireHeap(heapSize int) []asapmsgpack.HeapItem {
	if s.SS == nil {
		return nil
	}
	candidates := s.SS.Candidates()
	items := make([]asapmsgpack.HeapItem, 0, len(candidates))
	for _, key := range candidates {
		c := s.EstimateStringCount(key)
		if c <= 0 {
			continue
		}
		items = append(items, asapmsgpack.HeapItem{Key: key, Value: float64(c)})
	}
	// Highest count first; deterministic tie-break by key.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Value != items[j].Value {
			return items[i].Value > items[j].Value
		}
		return items[i].Key < items[j].Key
	})
	if len(items) > heapSize {
		items = items[:heapSize]
	}
	return items
}

// ApplyMsgpackWithHeapDelta applies a DELTA-HEAP frame (produced by
// SerializeMsgpackWithHeapDelta / asapmsgpack.MarshalCountSketchWithHeapDelta)
// onto `s` in place: the sparse signed cell deltas are added to s.Count, and
// s's downstream TopK heap is rebuilt from the frame's full heap. Cells
// outside s's dimensions are silently skipped (mirrors plain-delta ApplyDelta).
//
// This is the Go-side inverse used by the in-process forwarder runtime and
// round-trip tests; the backend (data_plane) applies the same frame via
// rmp_serde directly onto its stored matrix + heap.
func (s *CountSketch) ApplyMsgpackWithHeapDelta(buf []byte) error {
	_, _, cells, heap, _, err := asapmsgpack.UnmarshalCountSketchWithHeapDelta(buf)
	if err != nil {
		return fmt.Errorf("countsketch: apply msgpack-with-heap delta: %w", err)
	}
	for i := range cells {
		c := &cells[i]
		r, col := int(c.Row), int(c.Col)
		if r >= s.Rows || col >= s.Cols {
			continue
		}
		s.Count[r][col] += float64(c.DCount)
	}
	// Replace the downstream TopK heap with the frame's full heap.
	if s.TopK != nil {
		for _, it := range heap {
			s.TopK.Update(it.Key, int64(it.Value))
		}
	}
	return nil
}

// IsMsgpackWithHeapDelta reports whether buf is a DELTA-HEAP frame
// (4-element array with a leading `is_delta=true` marker), as opposed to a
// full heap frame (3-element array) or any other payload.
//
// It only PEEKS the framing prefix — the outer array header (which must encode
// length 4) followed by the first element being msgpack `true` (0xc3) — rather
// than fully decoding the matrix delta + heap (which on a large sparse frame is
// O(cells) work just to return a bool). The full frame is a 3-element array and
// any non-delta payload either has a different array length or a non-true first
// element, so this prefix is a sufficient and unambiguous discriminator (the
// two frame shapes are proven distinct by the wire tests).
func IsMsgpackWithHeapDelta(buf []byte) bool {
	// Outer array length must be exactly 4. rmp_serde emits a fixarray
	// (0x90|len) for our 4-element frame, but accept array16/array32 too for
	// robustness against any future re-encoding.
	if len(buf) < 2 {
		return false
	}
	var hdr int
	var rest []byte
	switch b := buf[0]; {
	case b&0xf0 == 0x90: // fixarray
		hdr = int(b & 0x0f)
		rest = buf[1:]
	case b == 0xdc: // array16
		if len(buf) < 3 {
			return false
		}
		hdr = int(buf[1])<<8 | int(buf[2])
		rest = buf[3:]
	case b == 0xdd: // array32
		if len(buf) < 5 {
			return false
		}
		hdr = int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])
		rest = buf[5:]
	default:
		return false
	}
	if hdr != 4 || len(rest) == 0 {
		return false
	}
	// First element must be msgpack true (the is_delta marker).
	return rest[0] == 0xc3
}

// DeserializeMsgpackWithHeapMatrix rebuilds a CountSketch (matrix only,
// empty heap) from a heap-bearing full MessagePack frame produced by
// SerializeMsgpackWithHeap / asapmsgpack.MarshalCountSketchWithHeap. Used as
// the delta BASE for the delta-heap path: ComputeDeltaAgainst receives the
// cached outbound base bytes (which, in heap-msgpack mode, are themselves a
// heap-bearing full frame — the empty base of the prior window), decodes its
// matrix here, and diffs the current sketch's matrix against it.
//
// Only the count matrix is needed for the matrix delta; the heap carried in
// the frame is ignored (the producer always ships its own current heap in
// the delta frame).
func DeserializeMsgpackWithHeapMatrix(buf []byte) (*CountSketch, error) {
	rowNum, colNum, matrix, _, _, err := asapmsgpack.UnmarshalCountSketchWithHeap(buf)
	if err != nil {
		return nil, fmt.Errorf("countsketch: msgpack-with-heap decode: %w", err)
	}
	s, err := NewCountSketch(int(rowNum), int(colNum))
	if err != nil {
		return nil, err
	}
	for r := 0; r < int(rowNum) && r < len(matrix); r++ {
		row := s.Count[r]
		src := matrix[r]
		for c := 0; c < int(colNum) && c < len(src); c++ {
			row[c] = src[c]
		}
	}
	return s, nil
}

// DeserializeMsgpack rebuilds a CountSketch from the cross-language
// MessagePack wire format produced by SerializeMsgpack (and by Rust's
// `sketch_core::count_sketch::CountSketch::serialize_msgpack`). Mirrors
// Rust's `deserialize_msgpack(bytes) -> Result<Self>`.
func DeserializeMsgpack(buf []byte) (*CountSketch, error) {
	rowNum, colNum, matrix, err := asapmsgpack.UnmarshalCountSketch(buf)
	if err != nil {
		return nil, fmt.Errorf("countsketch: msgpack decode: %w", err)
	}
	s, err := NewCountSketch(int(rowNum), int(colNum))
	if err != nil {
		return nil, err
	}
	for r := 0; r < int(rowNum) && r < len(matrix); r++ {
		row := s.Count[r]
		src := matrix[r]
		for c := 0; c < int(colNum) && c < len(src); c++ {
			row[c] = src[c]
		}
	}
	return s, nil
}
