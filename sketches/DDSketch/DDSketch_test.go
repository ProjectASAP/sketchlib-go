package ddsketch

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// ---------------- Helpers ----------------

func relErr(a, b float64) float64 {
	if a == 0 && b == 0 {
		return 0
	}
	return math.Abs(a-b) / math.Max(1e-30, math.Abs(b))
}

func trueQuantile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	k := int(math.Ceil(p*float64(n))) - 1
	if k < 0 {
		k = 0
	}
	if k >= n {
		k = n - 1
	}
	return sorted[k]
}

func sampleUniform(min, max float64, n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = min + r.Float64()*(max-min)
	}
	return out
}

// ---------------- Positive-only behavior ----------------

func TestPositiveOnlyBehavior(t *testing.T) {

	s := NewDDSketch(0.01)

	values := []float64{-100, 0, 10, 100, 1000}

	for _, v := range values {
		s.Add(v)
	}

	t.Logf("Inserted values: %v", values)
	t.Logf("Stored count (should ignore <=0): %d", s.GetCount())

	if s.GetCount() != 3 {
		t.Fatalf("expected count 3 (only positives)")
	}

	min, _ := s.GetValueAtQuantile(0)
	max, _ := s.GetValueAtQuantile(1)

	t.Logf("Min estimate: %f", min)
	t.Logf("Max estimate: %f", max)

	if min <= 0 {
		t.Fatalf("min should be strictly positive")
	}
}

// ---------------- Safe merge rejection ----------------

func TestSafeMergeRejectsDifferentMappings(t *testing.T) {

	s1 := NewDDSketch(0.01)
	s2 := NewDDSketch(0.02)

	s1.Add(100)
	s2.Add(100)

	err := s1.Merge(s2)

	t.Logf("Merge error: %v", err)

	if err == nil {
		t.Fatalf("expected merge failure")
	}
}

// ---------------- Safe merge success ----------------

func TestSafeMergeSuccess(t *testing.T) {

	s1 := NewDDSketch(0.01)
	s2 := NewDDSketch(0.01)

	values1 := []float64{10, 100}
	values2 := []float64{1000}

	for _, v := range values1 {
		s1.Add(v)
	}
	for _, v := range values2 {
		s2.Add(v)
	}

	err := s1.Merge(s2)
	if err != nil {
		t.Fatalf("merge should succeed")
	}

	t.Logf("Merged Count: %d", s1.GetCount())

	if s1.GetCount() != 3 {
		t.Fatalf("count mismatch after merge")
	}
}

// ---------------- Quantile accuracy (uniform) ----------------

func TestQuantileAccuracyUniform(t *testing.T) {

	const alpha = 0.01
	const n = 10000

	vals := sampleUniform(1000, 100000, n, 42)

	s := NewDDSketch(alpha)
	for _, v := range vals {
		s.Add(v)
	}

	sort.Float64s(vals)

	ps := []float64{0.1, 0.5, 0.9}

	t.Logf("=== Uniform Quantile Accuracy Test ===")

	for _, p := range ps {

		got, ok := s.GetValueAtQuantile(p)
		if !ok {
			t.Fatalf("quantile failed")
		}

		want := trueQuantile(vals, p)
		err := relErr(got, want)

		t.Logf(
			"p=%.2f | got=%10.3f | want=%10.3f | err=%.6f",
			p, got, want, err,
		)

		if err > alpha {
			t.Fatalf("relative error exceeded alpha")
		}
	}
}

// ---------------- Monotonic quantile test ----------------

func TestQuantileMonotonicity(t *testing.T) {

	s := NewDDSketch(0.01)

	values := []float64{10, 20, 30, 40, 50}
	for _, v := range values {
		s.Add(v)
	}

	prev := math.Inf(-1)

	for p := 0.0; p <= 1.0; p += 0.1 {

		val, ok := s.GetValueAtQuantile(p)
		if !ok {
			t.Fatalf("quantile failed")
		}

		t.Logf("p=%.2f → %f", p, val)

		if val < prev {
			t.Fatalf("quantiles not monotonic")
		}

		prev = val
	}
}
