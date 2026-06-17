package countminsketch

import (
	"reflect"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// TestSerializeMsgpackRoundTripViaAsapmsgpack pins the sketch's
// SerializeMsgpack output to what `wire/asapmsgpack.UnmarshalCountMinSketch`
// decodes. Indirectly pins it to ASAPQuery-backend's
// `sketch_core::count_min::CountMinSketch::deserialize_msgpack` via the
// golden-fixture tests in the wire package.
func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	// CountMinSketch requires power-of-two `cols` for its fast
	// bit-mask hash layout; pick cols=4 (2^2) so the constructor
	// accepts it.
	sketch, err := NewCountMinSketch(2, 4)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	// Populate the count matrix directly. `Count` is a []][]float64
	// view of the internal storage, so writing through it mutates
	// the same bytes the SerializeMsgpack path reads.
	for i, v := range []float64{1.0, 2.0, 3.0, 4.0} {
		sketch.Count[0][i] = v
	}
	for i, v := range []float64{5.0, 6.0, 7.0, 8.0} {
		sketch.Count[1][i] = v
	}

	bytes, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}
	if len(bytes) == 0 || bytes[0] != asapmsgpack.MagicCountMinSketch {
		t.Fatalf("expected magic-ID 0x%02x as first byte, got 0x%02x", asapmsgpack.MagicCountMinSketch, bytes[0])
	}

	// Strip the magic byte before feeding into the low-level unmarshal.
	matrix, rowNum, colNum, err := asapmsgpack.UnmarshalCountMinSketch(bytes[1:])
	if err != nil {
		t.Fatalf("UnmarshalCountMinSketch: %v", err)
	}
	if rowNum != 2 || colNum != 4 {
		t.Errorf("dims: got %dx%d, want 2x4", rowNum, colNum)
	}
	want := [][]float64{
		{1.0, 2.0, 3.0, 4.0},
		{5.0, 6.0, 7.0, 8.0},
	}
	if !reflect.DeepEqual(matrix, want) {
		t.Errorf("matrix mismatch\n  got  %v\n  want %v", matrix, want)
	}
}
