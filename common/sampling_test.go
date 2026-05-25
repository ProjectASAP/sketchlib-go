package common

import (
	"math"
	"testing"
)

func TestGeometricSamplerExactIsNoOp(t *testing.T) {
	s := NewGeometricSampler(1.0, 1)
	if !s.IsExact() {
		t.Fatal("p=1.0 sampler should be exact")
	}
	for i := 0; i < 1000; i++ {
		if !s.Admit() {
			t.Fatalf("exact sampler dropped update %d", i)
		}
	}
	if s.P() != 1.0 {
		t.Fatalf("exact sampler P()=%v want 1.0", s.P())
	}
}

func TestGeometricSamplerClampsAboveOne(t *testing.T) {
	s := NewGeometricSampler(2.0, 1)
	if !s.IsExact() || s.P() != 1.0 {
		t.Fatalf("p>1 should clamp to exact 1.0, got exact=%v p=%v", s.IsExact(), s.P())
	}
}

func TestGeometricSamplerAdmissionRate(t *testing.T) {
	for _, p := range []float64{0.5, 0.25, 0.1, 0.01} {
		s := NewGeometricSampler(p, 42)
		const n = 2_000_000
		admitted := 0
		for i := 0; i < n; i++ {
			if s.Admit() {
				admitted++
			}
		}
		rate := float64(admitted) / float64(n)
		// 4-sigma tolerance on a Binomial(n, p) admission rate.
		stddev := math.Sqrt(p * (1 - p) / float64(n))
		tol := 4 * stddev
		if math.Abs(rate-p) > tol+1e-6 {
			t.Errorf("p=%v: admission rate %.5f deviates from p by more than %.5f", p, rate, tol)
		}
	}
}

func TestGeometricSamplerReproducible(t *testing.T) {
	a := NewGeometricSampler(0.2, 7)
	b := NewGeometricSampler(0.2, 7)
	for i := 0; i < 10_000; i++ {
		if a.Admit() != b.Admit() {
			t.Fatalf("same-seed samplers diverged at update %d", i)
		}
	}
}

func TestGeometricSamplerDegeneratePIsUsable(t *testing.T) {
	// p<=0 must not drop the whole stream (it clamps to a tiny positive p).
	s := NewGeometricSampler(0, 1)
	if s.IsExact() {
		t.Fatal("p=0 should not be exact")
	}
	// Should not panic; admit count is allowed to be 0.
	for i := 0; i < 100; i++ {
		_ = s.Admit()
	}
}
