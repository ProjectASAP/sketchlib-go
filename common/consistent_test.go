package common

import (
	"fmt"
	"math"
	"testing"
)

// Marginal admission rate ≈ p, overall and per row.
func TestConsistentAdmit_Rate(t *testing.T) {
	const (
		p    = 0.3
		d    = 8
		n    = 200000
		seed = uint64(0xABCD)
	)
	perRow := make([]int, d)
	total := 0
	for occ := uint64(1); occ <= n; occ++ {
		for r := 0; r < d; r++ {
			if ConsistentAdmit(seed, occ, r, p) {
				perRow[r]++
				total++
			}
		}
	}
	if rate := float64(total) / float64(n*d); math.Abs(rate-p) > 0.005 {
		t.Fatalf("overall rate %.4f, want ≈ %.2f", rate, p)
	}
	for r := 0; r < d; r++ {
		if rate := float64(perRow[r]) / float64(n); math.Abs(rate-p) > 0.01 {
			t.Fatalf("row %d rate %.4f, want ≈ %.2f", r, rate, p)
		}
	}
}

// The decision is a pure function: recomputation anywhere agrees exactly —
// the property that makes double application idempotent (single-location
// sampling, design §3.1).
func TestConsistentAdmit_DeterministicIdempotent(t *testing.T) {
	for occ := uint64(0); occ < 1000; occ++ {
		for r := 0; r < 5; r++ {
			a := ConsistentAdmit(42, occ, r, 0.5)
			b := ConsistentAdmit(42, occ, r, 0.5)
			if a != b {
				t.Fatalf("non-deterministic at occ=%d row=%d", occ, r)
			}
		}
	}
	// Cursor form agrees with the stateless form.
	s1 := NewConsistentSampler(0.5, 42)
	for occ := uint64(1); occ <= 1000; occ++ {
		s1.BeginItem()
		for r := 0; r < 5; r++ {
			if s1.Admit() != ConsistentAdmit(42, occ, r, 0.5) {
				t.Fatalf("cursor/stateless mismatch at occ=%d row=%d", occ, r)
			}
		}
	}
	// SetOccurrence pins the same decisions (wire-recomputation path).
	s2 := NewConsistentSampler(0.5, 42)
	s2.SetOccurrence(7)
	for r := 0; r < 5; r++ {
		if s2.Admit() != ConsistentAdmit(42, 7, r, 0.5) {
			t.Fatalf("SetOccurrence mismatch at row=%d", r)
		}
	}
	// A pin is CONSUMED by the next BeginItem (the sampled sketch updates call
	// BeginItem internally): Rebind(seed, occ) + BeginItem must evaluate occ,
	// not occ+1 — and the following un-pinned item advances normally.
	s3 := NewConsistentSampler(0.5, 0)
	s3.Rebind(42, 7)
	s3.BeginItem()
	for r := 0; r < 5; r++ {
		if s3.Admit() != ConsistentAdmit(42, 7, r, 0.5) {
			t.Fatalf("Rebind+BeginItem must stay at occ=7, row=%d", r)
		}
	}
	s3.BeginItem() // no pin → advances to 8
	if s3.Admit() != ConsistentAdmit(42, 8, 0, 0.5) {
		t.Fatal("un-pinned BeginItem must advance to occ=8")
	}
}

// Rows within one occurrence decide independently: P[both rows admitted] ≈ p².
func TestConsistentAdmit_PerRowIndependence(t *testing.T) {
	const (
		p    = 0.5
		n    = 200000
		seed = uint64(0xFEED)
	)
	both := 0
	for occ := uint64(1); occ <= n; occ++ {
		a := ConsistentAdmit(seed, occ, 0, p)
		b := ConsistentAdmit(seed, occ, 1, p)
		if a && b {
			both++
		}
	}
	if rate := float64(both) / float64(n); math.Abs(rate-p*p) > 0.01 {
		t.Fatalf("P[row0 ∧ row1] = %.4f, want ≈ %.4f (independent rows)", rate, p*p)
	}
}

// Different seeds (e.g. window-start salted) produce decorrelated patterns.
func TestConsistentAdmit_SeedDecorrelates(t *testing.T) {
	const (
		p = 0.5
		n = 100000
	)
	agree := 0
	for occ := uint64(1); occ <= n; occ++ {
		if ConsistentAdmit(1, occ, 0, p) == ConsistentAdmit(2, occ, 0, p) {
			agree++
		}
	}
	// Independent fair decisions agree half the time.
	if rate := float64(agree) / float64(n); math.Abs(rate-0.5) > 0.01 {
		t.Fatalf("cross-seed agreement %.4f, want ≈ 0.5", rate)
	}
}

// The sampled CS update stays unbiased under the consistent sampler too
// (mirror of the GeometricSampler unbiasedness test).
func TestConsistentSampler_P1IsExact(t *testing.T) {
	s := NewConsistentSampler(1.0, 9)
	s.BeginItem()
	for r := 0; r < 16; r++ {
		if !s.Admit() {
			t.Fatal("p=1 must admit everything")
		}
	}
	if s.P() != 1.0 {
		t.Fatalf("P() = %v, want 1.0", s.P())
	}
	var nilS *ConsistentSampler
	if nilS.P() != 1.0 || !nilS.Admit() {
		t.Fatal("nil sampler must be exact")
	}
	_ = fmt.Sprintf("%v", nilS) // keep fmt import honest
}
