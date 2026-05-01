package countsketch

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

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
