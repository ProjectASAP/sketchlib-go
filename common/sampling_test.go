package common

import (
	"fmt"
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

func TestGeometricSamplerAdmitRowsMatchesScalarSequence(t *testing.T) {
	for _, rows := range []int{1, 3, 5, 16, 64} {
		for _, p := range []float64{1, 0.75, 0.1, 0.001} {
			scalar := NewGeometricSampler(p, 9182)
			jump := NewGeometricSampler(p, 9182)
			for item := 0; item < 100_000; item++ {
				var want uint64
				for row := 0; row < rows; row++ {
					if scalar.Admit() {
						want |= uint64(1) << uint(row)
					}
				}
				if got := jump.AdmitRows(rows); got != want {
					t.Fatalf("rows=%d p=%v item=%d: AdmitRows=%#x, scalar=%#x", rows, p, item, got, want)
				}
			}
		}
	}
}

func TestGeometricSamplerAdmitRowsSkipsWholeItems(t *testing.T) {
	s := NewGeometricSampler(0.5, 1)
	s.skip = 1_000_003
	if got := s.AdmitRows(5); got != 0 {
		t.Fatalf("AdmitRows returned %#x while gap spans the whole item", got)
	}
	if want := int64(999_998); s.skip != want {
		t.Fatalf("remaining gap=%d, want %d", s.skip, want)
	}
}

func BenchmarkGeometricSamplerRows(b *testing.B) {
	for _, p := range []float64{0.1, 0.01} {
		b.Run(fmt.Sprintf("scalar/p=%g", p), func(b *testing.B) {
			s := NewGeometricSampler(p, 42)
			for i := 0; i < b.N; i++ {
				for row := 0; row < 5; row++ {
					_ = s.Admit()
				}
			}
		})
		b.Run(fmt.Sprintf("jump/p=%g", p), func(b *testing.B) {
			s := NewGeometricSampler(p, 42)
			for i := 0; i < b.N; i++ {
				_ = s.AdmitRows(5)
			}
		})
	}
}
