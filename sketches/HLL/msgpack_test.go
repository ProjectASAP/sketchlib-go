package hll

import (
	"bytes"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// TestSerializeMsgpackRoundTripViaAsapmsgpack round-trips the sketch through the
// ASAPv1-aligned wire format: the variant is carried in the 2-byte kind_id
// (Datafusion -> Ertl-MLE, 0x01 0x02), the precision in the metadata, and the
// registers as a msgpack `bin` payload.
func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	sketch := NewHyperLogLog()
	for i := 0; i < 1000; i++ {
		sketch.UpdateValue(float64(i))
	}

	buf, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}

	// The envelope kind_id is the 2-byte Ertl-MLE ("Datafusion") id.
	kindID, _, err := asapmsgpack.DecodeWrapper(buf)
	if err != nil {
		t.Fatalf("DecodeWrapper: %v", err)
	}
	if !bytes.Equal(kindID, asapmsgpack.HLLKindErtlMLE) {
		t.Fatalf("expected kind_id %x, got %x", asapmsgpack.HLLKindErtlMLE, kindID)
	}

	state, err := asapmsgpack.UnmarshalHLLSketch(buf)
	if err != nil {
		t.Fatalf("UnmarshalHLLSketch: %v", err)
	}
	if state.Variant != asapmsgpack.HLLVariantDatafusion {
		t.Errorf("variant: got %q, want Datafusion", state.Variant)
	}
	if state.Precision != uint32(HLLPrecision) {
		t.Errorf("precision: got %d, want %d", state.Precision, HLLPrecision)
	}
	expectedRegisters := 1 << HLLPrecision
	if len(state.Registers) != expectedRegisters {
		t.Errorf("register count: got %d, want %d", len(state.Registers), expectedRegisters)
	}
	var nonZero int
	for _, r := range state.Registers {
		if r != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("expected some non-zero registers after 1000 inserts; got all zero")
	}

	// Full round-trip back into a sketch preserves the registers.
	decoded, err := DeserializeMsgpack(buf)
	if err != nil {
		t.Fatalf("DeserializeMsgpack: %v", err)
	}
	if !bytes.Equal(decoded.RegisterSlice(), sketch.RegisterSlice()) {
		t.Error("registers changed across round trip")
	}
}

func TestSerializeMsgpackEmptyRegisters(t *testing.T) {
	sketch := NewHyperLogLog()
	buf, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}

	kindID, _, err := asapmsgpack.DecodeWrapper(buf)
	if err != nil {
		t.Fatalf("DecodeWrapper: %v", err)
	}
	if !bytes.Equal(kindID, asapmsgpack.HLLKindErtlMLE) {
		t.Fatalf("expected kind_id %x, got %x", asapmsgpack.HLLKindErtlMLE, kindID)
	}

	state, err := asapmsgpack.UnmarshalHLLSketch(buf)
	if err != nil {
		t.Fatalf("UnmarshalHLLSketch: %v", err)
	}
	if state.Precision != uint32(HLLPrecision) {
		t.Errorf("precision: got %d, want %d", state.Precision, HLLPrecision)
	}
	if len(state.Registers) != 1<<HLLPrecision {
		t.Errorf("register count: got %d, want %d", len(state.Registers), 1<<HLLPrecision)
	}
	for i, r := range state.Registers {
		if r != 0 {
			t.Errorf("register %d: got %d, want 0", i, r)
			break
		}
	}
}
