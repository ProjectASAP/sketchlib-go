package countsketch

import (
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
