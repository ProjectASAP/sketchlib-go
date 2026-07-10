package countminsketch

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// SerializeMsgpack emits the MessagePack wire format ASAPQuery-backend
// decodes when the modified-OTLP `CountMinSketchDataPoint.encoding` is
// `COUNT_MIN_SKETCH_ENCODING_MSGPACK = 3` (cross-language contract with
// `sketch_core::count_min::CountMinSketch::deserialize_msgpack` on the
// Rust side). Pairs with the existing `SerializeProtoBytes` path — the
// two are interchangeable on the wire; the choice is made by the
// DataCollector processor via a config option and recorded in the
// data point's `encoding` field.
//
// Only the `Count` matrix is transmitted (not `Sum` / `Sum2`) because
// the Rust consumer's `CountMinSketch::deserialize_msgpack` path
// consumes the legacy Arroyo `WireFormat { sketch, row_num, col_num }`
// shape which has only the count matrix. Upstreams that need Sum /
// Sum2 semantics should keep using `SerializeProtoBytes`, which routes
// through the sketchlib `CountMinState` proto that does carry them.
func (s *CountMinSketch) SerializeMsgpack() ([]byte, error) {
	// MarshalCountMinSketch returns the complete ASAPv1 envelope (kind_id
	// CMSKind, metadata carrying counter_type/mode, payload [rows, cols,
	// counts]), so no separate EncodeWrapper call is needed.
	return asapmsgpack.MarshalCountMinSketch(
		uint64(s.Rows),
		uint64(s.Cols),
		s.Count,
	)
}

// DeserializeMsgpack rebuilds a CountMinSketch from the cross-language
// MessagePack wire format produced by SerializeMsgpack (and by Rust's
// `sketch_core::count_min::CountMinSketch::serialize_msgpack`). Mirrors
// Rust's `deserialize_msgpack(bytes) -> Result<Self>`.
//
// Only the count matrix round-trips through this format (Sum / Sum2 are
// not part of the msgpack wire shape; use SerializeProtoBytes if those
// must be preserved).
func DeserializeMsgpack(buf []byte) (*CountMinSketch, error) {
	matrix, rowNum, colNum, err := asapmsgpack.UnmarshalCountMinSketch(buf)
	if err != nil {
		return nil, fmt.Errorf("countminsketch: msgpack decode: %w", err)
	}
	s, err := NewCountMinSketch(int(rowNum), int(colNum))
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
