package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func fractionalCMS(t *testing.T, n int, seed int64) *CountMinSketch {
	t.Helper()
	cm, err := NewCountMinSketch(4, 512)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	s := common.NewGeometricSampler(0.3, seed) // w = 1/0.3 — non-integral
	for i := 0; i < n; i++ {
		cm.InsertWithHashSampledPerRow(common.FromString(fmt.Sprintf("k%d", i%97)).Hash, s)
	}
	return cm
}

// A sampled (fractional-count) CMS must round-trip both proto frames (full and
// Frequency-Only) losslessly via the counts_float wire.
func TestFloatWire_CMSFullAndFOLossless(t *testing.T) {
	cm := fractionalCMS(t, 5000, 7)

	for name, ser := range map[string]func() ([]byte, error){
		"full": cm.SerializeProtoBytes,
		"fo":   cm.SerializeProtoBytesFO,
	} {
		data, err := ser()
		if err != nil {
			t.Fatalf("%s serialize: %v", name, err)
		}
		back, err := DeserializeCountMinSketchFromProtoBytes(data)
		if err != nil {
			t.Fatalf("%s deserialize: %v", name, err)
		}
		for r := 0; r < cm.Rows; r++ {
			for c := 0; c < cm.Cols; c++ {
				if back.Count[r][c] != cm.Count[r][c] {
					t.Fatalf("%s cell (%d,%d) not lossless: %v vs %v", name, r, c, back.Count[r][c], cm.Count[r][c])
				}
			}
		}
	}
}

// Fractional deltas ride the float wire and reconstruct exactly:
// base + delta == current (threshold 0 → lossless), including Sum/Sum2.
func TestFloatWire_CMSDeltaRoundtripFractional(t *testing.T) {
	base := fractionalCMS(t, 2000, 42)
	snapBytes, _ := base.SerializeProtoBytes()
	snap, _ := DeserializeCountMinSketchFromProtoBytes(snapBytes)

	s := common.NewGeometricSampler(0.3, 43)
	for i := 0; i < 2000; i++ {
		base.InsertWithHashSampledPerRow(common.FromString(fmt.Sprintf("k%d", i%53)).Hash, s)
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
			if math.Abs(snap.Sum[r][c]-base.Sum[r][c]) > 1e-9 || math.Abs(snap.Sum2[r][c]-base.Sum2[r][c]) > 1e-9 {
				t.Fatalf("Sum/Sum2 drift at (%d,%d)", r, c)
			}
		}
	}
}
