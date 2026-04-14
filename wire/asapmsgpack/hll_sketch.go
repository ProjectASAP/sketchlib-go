package asapmsgpack

import "fmt"

// HLLVariant mirrors the Rust `sketch_core::hll_sketch::HllVariant`
// enum. Values are serialized as MessagePack strings matching the
// Rust variant names — rmp_serde's default for unit enums.
type HLLVariant string

const (
	HLLVariantUnspecified HLLVariant = "Unspecified"
	HLLVariantRegular     HLLVariant = "Regular"
	HLLVariantDatafusion  HLLVariant = "Datafusion"
	HLLVariantHip         HLLVariant = "Hip"
)

// HLLSketchState mirrors the Rust `sketch_core::hll_sketch::HllSketch`
// struct field-for-field. Field order is load-bearing.
type HLLSketchState struct {
	Variant   HLLVariant
	Precision uint32
	// Registers length MUST equal 1 << Precision; the receiver rejects
	// mismatched sizes.
	Registers []byte
	HipKxq0   float64
	HipKxq1   float64
	HipEst    float64
}

// MarshalHLLSketch emits the MessagePack payload that the Rust
// consumer's `sketch_core::hll_sketch::HllSketch::deserialize_msgpack`
// accepts when the modified-OTLP `HLLSketchDataPoint.encoding` is set
// to `HLL_SKETCH_ENCODING_MSGPACK = 3`.
//
// Wire format (rmp_serde compact mode on Rust `HllSketch`):
//
//	[
//	  variant    : string         // "Regular" / "Datafusion" / "Hip" / "Unspecified"
//	  precision  : uint32
//	  registers  : []uint8        // NOTE: array-of-u8, not msgpack `bin`
//	  hip_kxq0   : float64
//	  hip_kxq1   : float64
//	  hip_est    : float64
//	]
//
// Register count must be 1 << precision. HIP fields are only meaningful
// when Variant == HLLVariantHip; other variants should leave them at 0.
func MarshalHLLSketch(s HLLSketchState) ([]byte, error) {
	expected := 1 << s.Precision
	if len(s.Registers) != expected {
		return nil, fmt.Errorf(
			"asapmsgpack: HLLSketch has %d registers, expected 2^precision = %d",
			len(s.Registers), expected)
	}
	if s.Variant == "" {
		return nil, fmt.Errorf(
			"asapmsgpack: HLLSketch Variant must be set explicitly")
	}
	e := newEncoder()
	e.writeArrayLen(6)
	e.writeString(string(s.Variant))
	e.writeUint(uint64(s.Precision))
	e.writeU8Array(s.Registers)
	e.writeFloat64(s.HipKxq0)
	e.writeFloat64(s.HipKxq1)
	e.writeFloat64(s.HipEst)
	return e.bytes(), nil
}

// UnmarshalHLLSketch is the inverse of MarshalHLLSketch.
func UnmarshalHLLSketch(buf []byte) (HLLSketchState, error) {
	var s HLLSketchState
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	if n != 6 {
		return s, errWrongLen("HLLSketch", 6, n)
	}
	variant, err := d.readString()
	if err != nil {
		return s, err
	}
	s.Variant = HLLVariant(variant)
	prec, err := d.readUint()
	if err != nil {
		return s, err
	}
	s.Precision = uint32(prec)
	if s.Registers, err = d.readU8Array(); err != nil {
		return s, err
	}
	if s.HipKxq0, err = d.readFloat64(); err != nil {
		return s, err
	}
	if s.HipKxq1, err = d.readFloat64(); err != nil {
		return s, err
	}
	if s.HipEst, err = d.readFloat64(); err != nil {
		return s, err
	}
	if err := d.done(); err != nil {
		return s, err
	}
	return s, nil
}

func errWrongLen(sketch string, want, got int) error {
	return fmt.Errorf(
		"asapmsgpack: %s expected %d-element array, got %d",
		sketch, want, got)
}
