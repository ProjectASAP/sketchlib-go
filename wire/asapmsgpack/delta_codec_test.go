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

func TestHLLDeltaGoldenAndRoundTrip(t *testing.T) {
	regIdx := []uint32{0, 17, 1000}
	regVal := []uint8{3, 12, 255}
	golden := []byte{
		0x92,                               // array2
		0x93, 0x00, 0x11, 0xcd, 0x03, 0xe8, // regIdx: [0, 17, 1000]
		0x93, 0x03, 0x0c, 0xcc, 0xff, // regVal: [3, 12, 255]
	}
	got, err := MarshalHLLDelta(regIdx, regVal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("HLL golden mismatch:\n got  %x\n want %x", got, golden)
	}
	gi, gv, err := UnmarshalHLLDelta(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gi, regIdx) || !reflect.DeepEqual(gv, regVal) {
		t.Fatalf("HLL round-trip mismatch: regIdx=%v regVal=%v", gi, gv)
	}
}

func TestCountMinDeltaGoldenAndRoundTrip(t *testing.T) {
	rows, cols := uint64(3), uint64(8)
	rowIdx := []uint32{0, 2}
	colIdx := []uint32{1, 7}
	dCount := []int64{5, 100_000}
	golden := []byte{
		0x95,       // array5
		0x03, 0x08, // rows=3, cols=8
		0x92, 0x00, 0x02, // rowIdx: [0, 2]
		0x92, 0x01, 0x07, // colIdx: [1, 7]
		0x92, 0x05, 0xce, 0x00, 0x01, 0x86, 0xa0, // dCount: [5, 100000]
	}
	got, err := MarshalCountMinDelta(rows, cols, rowIdx, colIdx, dCount)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("CountMin golden mismatch:\n got  %x\n want %x", got, golden)
	}
	r, c, gr, gc, gd, err := UnmarshalCountMinDelta(got)
	if err != nil {
		t.Fatal(err)
	}
	if r != rows || c != cols || !reflect.DeepEqual(gr, rowIdx) ||
		!reflect.DeepEqual(gc, colIdx) || !reflect.DeepEqual(gd, dCount) {
		t.Fatalf("CountMin round-trip mismatch: %d %d %v %v %v", r, c, gr, gc, gd)
	}
}

func TestCountSketchCellDeltaGoldenAndRoundTrip(t *testing.T) {
	rows, cols := uint64(3), uint64(8)
	rowIdx := []uint32{0, 2}
	colIdx := []uint32{1, 7}
	dCount := []int64{5, -3} // signed cells
	golden := []byte{
		0x95,       // array5
		0x03, 0x08, // rows=3, cols=8
		0x92, 0x00, 0x02, // rowIdx: [0, 2]
		0x92, 0x01, 0x07, // colIdx: [1, 7]
		0x92, 0x05, 0xfd, // dCount: [5, -3]
	}
	got, err := MarshalCountSketchCellDelta(rows, cols, rowIdx, colIdx, dCount)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("CountSketch cell-delta golden mismatch:\n got  %x\n want %x", got, golden)
	}
	r, c, gr, gc, gd, err := UnmarshalCountSketchCellDelta(got)
	if err != nil {
		t.Fatal(err)
	}
	if r != rows || c != cols || !reflect.DeepEqual(gr, rowIdx) ||
		!reflect.DeepEqual(gc, colIdx) || !reflect.DeepEqual(gd, dCount) {
		t.Fatalf("CountSketch round-trip mismatch: %d %d %v %v %v", r, c, gr, gc, gd)
	}
}

func TestDeltaLengthMismatchErrors(t *testing.T) {
	if _, err := MarshalDDSketchDelta([]int32{1}, []uint64{1, 2}); err == nil {
		t.Fatal("DDSketch: expected length-mismatch error")
	}
	if _, err := MarshalHLLDelta([]uint32{1, 2}, []uint8{1}); err == nil {
		t.Fatal("HLL: expected length-mismatch error")
	}
	if _, err := MarshalCountMinDelta(1, 1, []uint32{0}, []uint32{0, 1}, []int64{1}); err == nil {
		t.Fatal("CountMin: expected length-mismatch error")
	}
}
