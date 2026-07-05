package countsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// Per-row sampling stays unbiased: inserting a heavy key N times at p=0.5 should
// estimate ≈ N (the 1/p weighting corrects for the admitted fraction).
func TestSampledPerRow_UnbiasedHeavyKey(t *testing.T) {
	const (
		n = 20000
		p = 0.5
	)
	cs, err := NewCountSketch(9, 4096)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	s := common.NewGeometricSampler(p, 12345)
	// spread background mass so the estimator exercises real rows/collisions
	for i := 0; i < n; i++ {
		cs.UpdateStringSampledPerRow("hot", 1.0, s)
		cs.UpdateStringSampledPerRow(fmt.Sprintf("bg%d", i%500), 1.0, s)
	}
	est := float64(cs.EstimateStringCount("hot"))
	rel := math.Abs(est-float64(n)) / float64(n)
	if rel > 0.15 {
		t.Fatalf("estimate %.0f vs %d (rel %.3f) — per-row sampling should stay unbiased", est, n, rel)
	}
}

// p ≥ 1 (or nil sampler) degenerates to the plain full update.
func TestSampledPerRow_FullRateEqualsPlain(t *testing.T) {
	a, _ := NewCountSketch(5, 1024)
	b, _ := NewCountSketch(5, 1024)
	full := common.NewGeometricSampler(1.0, 1)
	for i := 0; i < 1000; i++ {
		k := fmt.Sprintf("k%d", i%50)
		a.UpdateStringSampledPerRow(k, 1.0, full) // p=1 → plain
		b.UpdateString(k, 1.0)
	}
	for r := 0; r < a.Rows; r++ {
		for c := 0; c < a.Cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("p=1 must equal plain UpdateString at (%d,%d): %v vs %v", r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}

// The stateless ConsistentSampler keeps the same unbiasedness as the geometric
// sampler (same marginal rate p per (item,row) candidate, same 1/p weight).
func TestSampledPerRow_ConsistentSamplerUnbiased(t *testing.T) {
	const (
		n = 20000
		p = 0.5
	)
	cs, err := NewCountSketch(9, 4096)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	s := common.NewConsistentSampler(p, 12345)
	for i := 0; i < n; i++ {
		cs.UpdateStringSampledPerRow("hot", 1.0, s)
		cs.UpdateStringSampledPerRow(fmt.Sprintf("bg%d", i%500), 1.0, s)
	}
	est := float64(cs.EstimateStringCount("hot"))
	rel := math.Abs(est-float64(n)) / float64(n)
	if rel > 0.15 {
		t.Fatalf("estimate %.0f vs %d (rel %.3f) — consistent sampling should stay unbiased", est, n, rel)
	}
}

// nil sampler also degenerates to plain.
func TestSampledPerRow_NilSampler(t *testing.T) {
	cs, _ := NewCountSketch(5, 256)
	cs.UpdateStringSampledPerRow("x", 3.0, nil)
	if got := float64(cs.EstimateStringCount("x")); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("nil sampler should apply the full update, got %v", got)
	}
}
