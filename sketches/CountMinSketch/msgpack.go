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
	payload, err := asapmsgpack.MarshalCountMinSketch(
		uint64(s.Rows),
		uint64(s.Cols),
		s.Count,
	)
	if err != nil {
		return nil, err
	}
	return append([]byte{asapmsgpack.MagicCountMinSketch}, payload...), nil
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
	if len(buf) == 0 || buf[0] != asapmsgpack.MagicCountMinSketch {
		got := byte(0)
		if len(buf) > 0 {
			got = buf[0]
		}
		return nil, fmt.Errorf("countminsketch: msgpack magic-ID mismatch: expected 0x%02x, got 0x%02x", asapmsgpack.MagicCountMinSketch, got)
	}
	matrix, rowNum, colNum, err := asapmsgpack.UnmarshalCountMinSketch(buf[1:])
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
