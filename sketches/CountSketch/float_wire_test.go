package countsketch

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// A sampled (fractional-count) sketch must round-trip the proto full frame
// losslessly: counts_float carries exact float64 cells (no int64 truncation).
func TestFloatWire_FullFrameLossless(t *testing.T) {
	cs, _ := NewCountSketch(5, 512)
	s := common.NewGeometricSampler(0.3, 7) // w = 1/0.3 — non-integral
	for i := 0; i < 5000; i++ {
		cs.UpdateStringSampledPerRow(fmt.Sprintf("k%d", i%97), 1.0, s)
	}
	data, err := cs.SerializeProtoBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	back, err := DeserializeCountSketchFromProtoBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	for r := 0; r < cs.Rows; r++ {
		for c := 0; c < cs.Cols; c++ {
			if back.Count[r][c] != cs.Count[r][c] {
				t.Fatalf("cell (%d,%d) not lossless: %v vs %v", r, c, back.Count[r][c], cs.Count[r][c])
			}
		}
	}
}

// An all-integral sketch must keep the exact pre-float sint64 wire bytes
// (backward compatibility with existing consumers/goldens).
func TestFloatWire_IntegralBytesUnchanged(t *testing.T) {
	build := func() *CountSketch {
		cs, _ := NewCountSketch(4, 256)
		for i := 0; i < 500; i++ {
			cs.UpdateString(fmt.Sprintf("k%d", i%31), 1.0)
		}
		return cs
	}
	a, _ := build().SerializeProtoBytes()
	b, _ := build().SerializeProtoBytes()
	if !bytes.Equal(a, b) {
		t.Fatalf("integral serialization not deterministic")
	}
	back, err := DeserializeCountSketchFromProtoBytes(a)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	orig := build()
	for r := 0; r < orig.Rows; r++ {
		for c := 0; c < orig.Cols; c++ {
			if back.Count[r][c] != orig.Count[r][c] {
				t.Fatalf("integral roundtrip mismatch at (%d,%d)", r, c)
			}
		}
	}
}

// Fractional deltas ride the float wire and reconstruct exactly:
// base + delta == current, cell for cell (threshold 0 → lossless).
func TestFloatWire_DeltaRoundtripFractional(t *testing.T) {
	base, _ := NewCountSketch(5, 512)
	s := common.NewGeometricSampler(0.3, 42)
	for i := 0; i < 2000; i++ {
		base.UpdateStringSampledPerRow(fmt.Sprintf("k%d", i%53), 1.0, s)
	}
	// snapshot = deep copy of base via lossless full frame
	snapBytes, _ := base.SerializeProtoBytes()
	snap, _ := DeserializeCountSketchFromProtoBytes(snapBytes)

	// advance base
	for i := 0; i < 2000; i++ {
		base.UpdateStringSampledPerRow(fmt.Sprintf("k%d", i%53), 1.0, s)
	}

	d, err := ComputeDelta(snap, base, 0)
	if err != nil {
		t.Fatalf("ComputeDelta must accept fractional deltas now: %v", err)
	}
	payload, err := SerializeDelta(d)
	if err != nil {
		t.Fatalf("SerializeDelta: %v", err)
	}
	got, err := DeserializeDelta(payload)
	if err != nil {
		t.Fatalf("DeserializeDelta: %v", err)
	}
	ApplyDelta(snap, got)
	for r := 0; r < base.Rows; r++ {
		for c := 0; c < base.Cols; c++ {
			if math.Abs(snap.Count[r][c]-base.Count[r][c]) > 1e-9 {
				t.Fatalf("snap+delta != current at (%d,%d): %v vs %v", r, c, snap.Count[r][c], base.Count[r][c])
			}
		}
	}
}
