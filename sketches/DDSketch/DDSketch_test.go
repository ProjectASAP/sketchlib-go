package ddsketch

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// ---------------- helpers ----------------

func relErr(a, b float64) float64 {
	if a == 0 && b == 0 {
		return 0
	}
	return math.Abs(a-b) / math.Max(1e-30, math.Abs(b))
}

func trueQuantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	n := len(sorted)
	k := int(math.Ceil(p*float64(n))) - 1
	if k < 0 {
		k = 0
	}
	if k >= n {
		k = n - 1
	}
	return sorted[k]
}

// ---------------- distributions ----------------

func sampleUniform(min, max float64, n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := min + r.Float64()*(max-min)
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		}
	}
	return out
}

func sampleExponential(lambda float64, n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = r.ExpFloat64() / lambda
	}
	return out
}

func sampleNormal(mean, std float64, n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := r.NormFloat64()*std + mean
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		}
	}
	return out
}

// ---------------- basic tests ----------------

func TestInsertAndQueryBasic(t *testing.T) {
	s := NewDDSketch(0.01)

	vals := []float64{0, -5, 1, 2, 3, 10, 50, 100, 1000}
	for _, v := range vals {
		s.Add(v)
	}

	if s.count != 7 {
		t.Fatalf("expected count 7, got %d", s.count)
	}

	ps := []float64{0, 0.5, 0.9, 0.99, 1}
	prev := math.Inf(-1)

	for _, p := range ps {
		q, ok := s.GetValueAtQuantile(p)
		if !ok {
			t.Fatalf("quantile returned false at p=%f", p)
		}
		if q < prev-1e-12 {
			t.Fatalf("non-monotone quantile at p=%f", p)
		}
		prev = q
	}
}

func TestEmptyQuantileReturnsFalse(t *testing.T) {
	s := NewDDSketch(0.01)

	if _, ok := s.GetValueAtQuantile(0.5); ok {
		t.Fatal("expected no quantile on empty sketch")
	}
	if s.count != 0 {
		t.Fatal("count should be zero")
	}
}

// ---------------- uniform distribution ----------------

func TestDDSUniformDistributionQuantiles(t *testing.T) {
	const alpha = 0.01

	quantiles := []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1}

	for idx, n := range []int{1000, 5000, 20000} {
		seed := int64(0xA5A50000 + idx)
		vals := sampleUniform(1_000_000, 10_000_000, n, seed)

		s := NewDDSketch(alpha)
		for _, v := range vals {
			s.Add(v)
		}

		sort.Float64s(vals)

		for _, q := range quantiles {
			got, ok := s.GetValueAtQuantile(q)
			if !ok {
				t.Fatalf("quantile failed at p=%f", q)
			}
			want := trueQuantile(vals, q)
			err := relErr(got, want)
			t.Logf(
				"[uniform] n=%d p=%.2f got=%.6f want=%.6f err=%.6f",
				n, q, got, want, err,
			)

			if err > alpha {
				t.Fatalf("quantile error exceeds tolerance")
			}
		}
	}
}

// ---------------- normal distribution ----------------

func TestDDSNormalDistributionQuantiles(t *testing.T) {
	const alpha = 0.01
	const mean = 1000
	const std = 100

	quantiles := []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1}

	for idx, n := range []int{1000, 5000, 20000} {
		seed := int64(0xC0DE0000 + idx)
		vals := sampleNormal(mean, std, n, seed)

		s := NewDDSketch(alpha)
		for _, v := range vals {
			s.Add(v)
		}

		sort.Float64s(vals)

		for _, q := range quantiles {
			got, _ := s.GetValueAtQuantile(q)
			want := trueQuantile(vals, q)

			if relErr(got, want) > alpha {
				t.Fatalf("normal p=%.2f got=%f want=%f", q, got, want)
			}
		}
	}
}

// ---------------- exponential distribution ----------------

func TestDDSExponentialDistributionQuantiles(t *testing.T) {
	const alpha = 0.01
	const lambda = 1e-3

	quantiles := []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1}

	for idx, n := range []int{1000, 5000, 20000} {
		seed := int64(0xE3E30000 + idx)
		vals := sampleExponential(lambda, n, seed)

		s := NewDDSketch(alpha)
		for _, v := range vals {
			s.Add(v)
		}

		sort.Float64s(vals)

		for _, q := range quantiles {
			got, _ := s.GetValueAtQuantile(q)
			want := trueQuantile(vals, q)
			if relErr(got, want) > 0.011 {
				t.Fatalf("exp p=%.2f got=%f want=%f", q, got, want)
			}
		}
	}
}

// ---------------- merge ----------------

func TestMergeTwoSketches(t *testing.T) {
	s1 := NewDDSketch(0.01)
	s2 := NewDDSketch(0.01)

	for _, v := range []float64{1, 2, 3, 4} {
		s1.Add(v)
	}
	for _, v := range []float64{5, 10, 20} {
		s2.Add(v)
	}

	if err := s1.Merge(s2); err != nil {
		t.Fatal(err)
	}

	if s1.count != 7 {
		t.Fatalf("expected count 7, got %d", s1.count)
	}
	if s1.min != 1 {
		t.Fatalf("min mismatch")
	}
	if s1.max != 20 {
		t.Fatalf("max mismatch")
	}

	if v, _ := s1.GetValueAtQuantile(0); v != 1 {
		t.Fatal("p0 mismatch")
	}
	if v, _ := s1.GetValueAtQuantile(1); v != 20 {
		t.Fatal("p1 mismatch")
	}
}
