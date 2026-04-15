package countminsketch

import (
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
	return asapmsgpack.MarshalCountMinSketch(
		uint64(s.Rows),
		uint64(s.Cols),
		s.Count,
	)
}
