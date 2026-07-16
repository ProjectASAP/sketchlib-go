package asapmsgpack

import (
	"bytes"
	"fmt"
)

// HLLVariant selects the HLL estimator. Under ASAPv1 the variant is the sketch's
// algorithm identity and is carried in the envelope `kind_id`
// (asap_sketchlib/docs/asapv1_wire_format.md §1), NOT in the payload.
type HLLVariant string

const (
	// HLLVariantUnspecified is the reserved/unset variant (not serializable).
	HLLVariantUnspecified HLLVariant = "Unspecified"
	// HLLVariantRegular is the Classic estimator (kind_id 0x01 0x01).
	HLLVariantRegular HLLVariant = "Regular"
	// HLLVariantDatafusion is the Ertl-MLE estimator (kind_id 0x01 0x02).
	HLLVariantDatafusion HLLVariant = "Datafusion"
	// HLLVariantHip is the HIP estimator (kind_id 0x01 0x03).
	HLLVariantHip HLLVariant = "Hip"
)

// HLLSketchState is the decoded state of an HLL sketch. Variant maps to the
// kind_id, Precision to the metadata, and the register/HIP fields to the
// payload.
type HLLSketchState struct {
	Variant   HLLVariant
	Precision uint32
	// Registers length MUST equal 1 << Precision.
	Registers []byte
	// HipKxq0/HipKxq1/HipEst are only present for Variant == HLLVariantHip.
	HipKxq0 float64
	HipKxq1 float64
	HipEst  float64
}

// variantKindID maps a variant to its ASAPv1 kind_id.
func variantKindID(v HLLVariant) ([]byte, error) {
	switch v {
	case HLLVariantRegular:
		return HLLKindClassic, nil
	case HLLVariantDatafusion:
		return HLLKindErtlMLE, nil
	case HLLVariantHip:
		return HLLKindHIP, nil
	case HLLVariantUnspecified, "":
		return nil, fmt.Errorf("asapmsgpack: HLL variant must be set (not %q)", v)
	default:
		return nil, fmt.Errorf("asapmsgpack: unknown HLL variant %q", v)
	}
}

// kindIDVariant is the inverse of variantKindID.
func kindIDVariant(kindID []byte) (HLLVariant, error) {
	switch {
	case bytes.Equal(kindID, HLLKindClassic):
		return HLLVariantRegular, nil
	case bytes.Equal(kindID, HLLKindErtlMLE):
		return HLLVariantDatafusion, nil
	case bytes.Equal(kindID, HLLKindHIP):
		return HLLVariantHip, nil
	default:
		return "", fmt.Errorf("asapmsgpack: kind_id %x is not an HLL variant", kindID)
	}
}

// MarshalHLLSketch encodes an HLL sketch into a complete ASAPv1 envelope
// (asap_sketchlib/docs/asapv1_wire_format.md §1/§2/§3.1). The layout is:
//
//	envelope[ kind_id = variant | metadata = { …hash spec…, canonical_seed_index,
//	          precision } | payload ]
//
// where the payload is a positional MessagePack array:
//
//	Classic / Ertl-MLE:  [ registers:bin ]
//	HIP:                 [ registers:bin, hip_kxq0:f64, hip_kxq1:f64, hip_est:f64 ]
//
// `registers` is MessagePack **bin** (one byte per register), matching Rust's
// serde_bytes encoding — NOT an array-of-int.
func MarshalHLLSketch(s HLLSketchState) ([]byte, error) {
	kindID, err := variantKindID(s.Variant)
	if err != nil {
		return nil, err
	}
	expected := 1 << s.Precision
	if len(s.Registers) != expected {
		return nil, fmt.Errorf(
			"asapmsgpack: HLLSketch has %d registers, expected 2^precision = %d",
			len(s.Registers), expected)
	}

	e := newEncoder()
	if s.Variant == HLLVariantHip {
		e.writeArrayLen(4)
		e.writeBin(s.Registers)
		e.writeFloat64(s.HipKxq0)
		e.writeFloat64(s.HipKxq1)
		e.writeFloat64(s.HipEst)
	} else {
		e.writeArrayLen(1)
		e.writeBin(s.Registers)
	}

	metadata := encodeHLLMetadata(s.Precision)
	return EncodeWrapper(kindID, metadata, e.bytes()), nil
}

// UnmarshalHLLSketch is the inverse of MarshalHLLSketch: it takes a complete
// ASAPv1 envelope and returns the decoded state, failing closed on any envelope,
// metadata (hash-spec) or payload inconsistency.
func UnmarshalHLLSketch(buf []byte) (HLLSketchState, error) {
	var s HLLSketchState

	kindID, metadata, payload, err := SplitWrapper(buf)
	if err != nil {
		return s, err
	}
	variant, err := kindIDVariant(kindID)
	if err != nil {
		return s, err
	}
	s.Variant = variant

	precision, err := decodeHLLMetadata(metadata)
	if err != nil {
		return s, err
	}
	s.Precision = precision

	d := newDecoder(payload)
	n, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	wantLen := 1
	if variant == HLLVariantHip {
		wantLen = 4
	}
	if n != wantLen {
		return s, errWrongLen("HLLSketch", wantLen, n)
	}
	if s.Registers, err = d.readBin(); err != nil {
		return s, err
	}
	if expected := 1 << s.Precision; len(s.Registers) != expected {
		return s, fmt.Errorf(
			"asapmsgpack: HLLSketch has %d registers, expected 2^precision = %d",
			len(s.Registers), expected)
	}
	if variant == HLLVariantHip {
		if s.HipKxq0, err = d.readFloat64(); err != nil {
			return s, err
		}
		if s.HipKxq1, err = d.readFloat64(); err != nil {
			return s, err
		}
		if s.HipEst, err = d.readFloat64(); err != nil {
			return s, err
		}
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
