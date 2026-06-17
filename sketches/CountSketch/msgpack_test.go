package countsketch

import (
	"reflect"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	sketch, err := NewCountSketch(2, 4)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	// Signed values — Count Sketch uses ±1 updates so the wire
	// format must preserve negatives.
	for i, v := range []float64{1.0, -2.0, 3.0, -4.0} {
		sketch.Count[0][i] = v
	}
	for i, v := range []float64{-5.0, 6.0, -7.0, 8.0} {
		sketch.Count[1][i] = v
	}

	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}
	if len(bytes) == 0 || bytes[0] != asapmsgpack.MagicCountSketch {
		t.Fatalf("expected magic-ID 0x%02x as first byte, got 0x%02x", asapmsgpack.MagicCountSketch, bytes[0])
	}

	// Strip the magic byte before feeding into the low-level unmarshal.
	rowNum, colNum, matrix, err := asapmsgpack.UnmarshalCountSketch(bytes[1:])
	if err != nil {
		t.Fatalf("UnmarshalCountSketch: %v", err)
	}
	if rowNum != 2 || colNum != 4 {
		t.Errorf("dims: got %dx%d, want 2x4", rowNum, colNum)
	}
	want := [][]float64{
		{1.0, -2.0, 3.0, -4.0},
		{-5.0, 6.0, -7.0, 8.0},
	}
	if !reflect.DeepEqual(matrix, want) {
		t.Errorf("matrix mismatch\n  got  %v\n  want %v", matrix, want)
	}
}
