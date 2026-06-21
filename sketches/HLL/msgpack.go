package hll

import (
	"errors"
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
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
	// backing storage through the returned payload. RegisterSlice() materialises
	// the dense array for a sparse instance (already a fresh copy).
	src := h.RegisterSlice()
	registers := make([]byte, len(src))
	copy(registers, src)

	payload, err := asapmsgpack.MarshalHLLSketch(asapmsgpack.HLLSketchState{
		Variant:   asapmsgpack.HLLVariantDatafusion,
		Precision: uint32(HLLPrecision),
		Registers: registers,
	})
	if err != nil {
		return nil, err
	}
	return asapmsgpack.EncodeWrapper([]byte{asapmsgpack.MagicHLL}, payload), nil
}

// DeserializeMsgpack rebuilds a HyperLogLog from the cross-language
// MessagePack wire format produced by SerializeMsgpack (and by Rust's
// `sketch_core::hll_sketch::HllSketch::serialize_msgpack`). Mirrors
// Rust's `deserialize_msgpack(bytes) -> Result<Self>`.
func DeserializeMsgpack(buf []byte) (*HyperLogLog, error) {
	kindID, payload, err := asapmsgpack.DecodeWrapper(buf)
	if err != nil {
		return nil, fmt.Errorf("hll: %w", err)
	}
	if len(kindID) != 1 || kindID[0] != asapmsgpack.MagicHLL {
		return nil, fmt.Errorf("hll: msgpack kind_id mismatch: expected [0x%02x], got %v", asapmsgpack.MagicHLL, kindID)
	}
	state, err := asapmsgpack.UnmarshalHLLSketch(payload)
	if err != nil {
		return nil, fmt.Errorf("hll: msgpack decode: %w", err)
	}
	if state.Precision != uint32(HLLPrecision) {
		return nil, fmt.Errorf("hll: precision mismatch: got %d, want %d", state.Precision, HLLPrecision)
	}
	if len(state.Registers) != HLLRegisterCount {
		return nil, errors.New("hll: invalid register length")
	}
	regs := make([]uint8, HLLRegisterCount)
	copy(regs, state.Registers)
	return &HyperLogLog{Registers: storage.Vector1DFromSlice(regs)}, nil
}
