package hll

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	sketch := NewHyperLogLog()
	// Insert a few distinct values so at least a handful of registers
	// end up non-zero.
	for i := 0; i < 1000; i++ {
		sketch.InsertValue(float64(i))
	}

	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}

	state, err := asapmsgpack.UnmarshalHLLSketch(bytes)
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
	// After 1000 inserts we should see at least a few non-zero
	// registers (pins that the bytes weren't lost in transit).
	var nonZero int
	for _, r := range state.Registers {
		if r != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("expected some non-zero registers after 1000 inserts; got all zero")
	}
}

func TestSerializeMsgpackEmptyRegisters(t *testing.T) {
	sketch := NewHyperLogLog()
	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}
	state, err := asapmsgpack.UnmarshalHLLSketch(bytes)
	if err != nil {
		t.Fatalf("UnmarshalHLLSketch: %v", err)
	}
	if state.Precision != uint32(HLLPrecision) {
		t.Errorf("precision: got %d, want %d", state.Precision, HLLPrecision)
	}
	if len(state.Registers) != 1<<HLLPrecision {
		t.Errorf("register count: got %d, want %d", len(state.Registers), 1<<HLLPrecision)
	}
	// Every register should be zero on a fresh HLL.
	for i, r := range state.Registers {
		if r != 0 {
			t.Errorf("register %d: got %d, want 0", i, r)
			break
		}
	}
}
