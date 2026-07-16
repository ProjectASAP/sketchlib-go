package countminsketch

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// TestSerializeMsgpackRoundTripViaAsapmsgpack pins the sketch's SerializeMsgpack
// output to the ASAPv1-aligned wire format: a single Count-Min kind_id
// (0x02 0x00), counter_type=f64 / mode=fast in the metadata, and a positional
// [rows, cols, counts] payload with counts packed row-major.
func TestSerializeMsgpackRoundTripViaAsapmsgpack(t *testing.T) {
	// CountMinSketch requires power-of-two `cols` for its fast bit-mask hash
	// layout; pick cols=4 (2^2) so the constructor accepts it.
	sketch, err := NewCountMinSketch(2, 4)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	for i, v := range []float64{1.0, 2.0, 3.0, 4.0} {
		sketch.Count[0][i] = v
	}
	for i, v := range []float64{5.0, 6.0, 7.0, 8.0} {
		sketch.Count[1][i] = v
	}

	buf, err := sketch.SerializeMsgpack()
	if err != nil {
		t.Fatalf("SerializeMsgpack: %v", err)
	}

	kindID, _, err := asapmsgpack.DecodeWrapper(buf)
	if err != nil {
		t.Fatalf("DecodeWrapper: %v", err)
	}
	if !bytes.Equal(kindID, asapmsgpack.CMSKind) {
		t.Fatalf("expected kind_id %x, got %x", asapmsgpack.CMSKind, kindID)
	}

	matrix, rowNum, colNum, err := asapmsgpack.UnmarshalCountMinSketch(buf)
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

	// Full round-trip back into a sketch preserves the count matrix.
	decoded, err := DeserializeMsgpack(buf)
	if err != nil {
		t.Fatalf("DeserializeMsgpack: %v", err)
	}
	if !reflect.DeepEqual(decoded.Count, want) {
		t.Errorf("count matrix changed across round trip: %v", decoded.Count)
	}
}
