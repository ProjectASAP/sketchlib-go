package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// Per-row sampling stays unbiased: inserting a heavy key N times at p=0.5 should
// estimate ≈ N (the in-place 1/p weighting corrects for the admitted fraction).
func TestInsertSampledPerRow_UnbiasedHeavyKey(t *testing.T) {
	const (
		n = 20000
		p = 0.5
	)
	cm, err := NewCountMinSketch(4, 4096)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	s := common.NewGeometricSampler(p, 12345)
	hot := common.FromString("hot").Hash
	for i := 0; i < n; i++ {
		cm.InsertWithHashSampledPerRow(hot, s)
		bg := common.FromString(fmt.Sprintf("bg%d", i%500)).Hash
		cm.InsertWithHashSampledPerRow(bg, s)
	}
	est := cm.FastEstimateWithHash(hot)
	rel := math.Abs(est-float64(n)) / float64(n)
	if rel > 0.15 {
		t.Fatalf("estimate %.0f vs %d (rel %.3f) — per-row sampling should stay unbiased", est, n, rel)
	}
}

// p >= 1 (full rate) degenerates to the plain InsertWithHash: identical cells.
func TestInsertSampledPerRow_FullRateEqualsPlain(t *testing.T) {
	a, _ := NewCountMinSketch(4, 1024)
	b, _ := NewCountMinSketch(4, 1024)
	full := common.NewGeometricSampler(1.0, 1)
	for i := 0; i < 1000; i++ {
		h := common.FromString(fmt.Sprintf("k%d", i%50)).Hash
		a.InsertWithHashSampledPerRow(h, full) // p=1 → plain
		b.InsertWithHash(h)
	}
	for r := 0; r < a.Rows; r++ {
		for c := 0; c < a.Cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("p=1 must equal plain InsertWithHash at (%d,%d): %v vs %v", r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}

// nil sampler also degenerates to the plain insert.
func TestInsertSampledPerRow_NilSampler(t *testing.T) {
	a, _ := NewCountMinSketch(4, 256)
	b, _ := NewCountMinSketch(4, 256)
	h := common.FromString("x").Hash
	a.InsertWithHashSampledPerRow(h, nil)
	b.InsertWithHash(h)
	for r := 0; r < a.Rows; r++ {
		for c := 0; c < a.Cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("nil sampler should apply the full insert at (%d,%d): %v vs %v", r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}
