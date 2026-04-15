package hll

import (
	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// SerializeMsgpack emits the MessagePack wire format ASAPQuery-backend
// decodes when the modified-OTLP `HLLSketchDataPoint.encoding` is
// `HLL_SKETCH_ENCODING_MSGPACK = 3` (cross-language contract with
// `sketch_core::hll_sketch::HllSketch::deserialize_msgpack`).
//
// The Go HLL implementation uses the **DataFusion** variant of the
// estimator family — the Register values are `leading_zeros + 1`
// computed from the hash, using Otmar Ertl's improved estimator at
// query time. We pin the variant field to `"Datafusion"` accordingly.
// This MUST match `sketch_core::hll_sketch::HllVariant::Datafusion`
// on the Rust side — if the Go implementation ever grows a
// non-DataFusion variant, this string needs to be selected per-instance.
//
// HIP (Historic Inverse Probability) accumulator components are not
// maintained by this implementation (those are specific to the HIP
// variant), so hip_kxq0 / hip_kxq1 / hip_est are all zero. The Rust
// consumer ignores these fields for non-HIP variants.
func (h *HyperLogLog) SerializeMsgpack() ([]byte, error) {
	// Clone the register bytes so the caller can't mutate the sketch's
	// backing storage through the returned payload.
	src := h.Registers.AsSlice()
	registers := make([]byte, len(src))
	copy(registers, src)

	return asapmsgpack.MarshalHLLSketch(asapmsgpack.HLLSketchState{
		Variant:   asapmsgpack.HLLVariantDatafusion,
		Precision: uint32(HLLPrecision),
		Registers: registers,
	})
}
