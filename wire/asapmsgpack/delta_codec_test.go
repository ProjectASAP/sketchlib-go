package asapmsgpack

import (
	"bytes"
	"reflect"
	"testing"
)

// Golden fixtures below are pinned to the asap_sketchlib (Rust) msgpack-delta
// output: for each fixture, `<Sketch>Delta::to_msgpack()` in
// `src/message_pack_format/portable/*.rs` produces exactly these bytes. If the
// Rust wire format ever shifts, these tests fail loudly.

func TestDDSketchDeltaGoldenAndRoundTrip(t *testing.T) {
	idx := []int32{-2, 5, 300}
	dCount := []uint64{1, 2, 1_000_000}
	golden := []byte{
		0x92,                               // array2
		0x93, 0xfe, 0x05, 0xcd, 0x01, 0x2c, // idx: [-2, 5, 300]
		0x93, 0x01, 0x02, 0xce, 0x00, 0x0f, 0x42, 0x40, // dCount: [1, 2, 1000000]
	}
	got, err := MarshalDDSketchDelta(idx, dCount)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("DDSketch golden mismatch:\n got  %x\n want %x", got, golden)
	}
	gi, gd, err := UnmarshalDDSketchDelta(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gi, idx) || !reflect.DeepEqual(gd, dCount) {
		t.Fatalf("DDSketch round-trip mismatch: idx=%v dCount=%v", gi, gd)
	}
}

func TestDeltaLengthMismatchErrors(t *testing.T) {
	if _, err := MarshalDDSketchDelta([]int32{1}, []uint64{1, 2}); err == nil {
		t.Fatal("DDSketch: expected length-mismatch error")
	}
}
