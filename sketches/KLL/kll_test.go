package kll

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// ======================
// Helpers
// ======================

func newTestKLL(t *testing.T, k int) *KLLSketch {
	s, err := NewKLLSketch(k)
	if err != nil {
		t.Fatalf("Failed to init KLLSketch(k=%d): %v", k, err)
	}
	return s
}

// ======================
// Basic Correctness
// ======================

func TestKLL_BasicFlow(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	if s.GetSize() != 0 {
		t.Fatalf("new sketch should be empty")
	}

	n := 100
	for i := 1; i <= n; i++ {
		s.Insert(float64(i))
	}

	if s.GetSize() != n {
		t.Fatalf("expected size %d, got %d", n, s.GetSize())
	}

	if s.Count() != n {
		t.Fatalf("expected count %d, got %d", n, s.Count())
	}

	rank := s.Rank(50.5)
	if rank != 50 {
		t.Fatalf("Rank(50.5): expected 50, got %d", rank)
	}
}

// ======================
// Statistical Accuracy
// ======================

func TestKLL_StatisticalAccuracy(t *testing.T) {
	k := 200
	n := 10_000
	maxError := 0.02 // 2%

	s := newTestKLL(t, k)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	data := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := rng.Float64()
		data = append(data, v)
		s.Insert(v)
	}

	sort.Float64s(data)

	checkPoints := []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99}
	maxObserved := 0.0

	for _, p := range checkPoints {
		idx := int(float64(n) * p)
		if idx >= n {
			idx = n - 1
		}
		val := data[idx]

		trueRank := float64(idx)
		estRank := float64(s.Rank(val))
		err := math.Abs(estRank-trueRank) / float64(n)

		t.Logf("[Rank] p=%.2f true=%.0f est=%.0f err=%.4f%%",
			p, trueRank, estRank, err*100)

		if err > maxObserved {
			maxObserved = err
		}
		if err > maxError {
			t.Fatalf("rank error %.4f%% exceeds limit %.4f%%", err*100, maxError*100)
		}
	}

	t.Logf("Max observed rank error: %.4f%%", maxObserved*100)
}

// ======================
// Merge Tests
// ======================

func TestKLL_Merge(t *testing.T) {
	k := 200
	s1 := newTestKLL(t, k)
	s2 := newTestKLL(t, k)

	for i := 0; i < 2000; i += 2 {
		s1.Insert(float64(i))
	}
	for i := 1; i < 2000; i += 2 {
		s2.Insert(float64(i))
	}

	if err := s1.Merge(s2); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if s1.GetSize() != 2000 {
		t.Fatalf("merged size expected 2000, got %d", s1.GetSize())
	}

	median := s1.CDF().Query(0.5)
	if median < 900 || median > 1100 {
		t.Fatalf("median out of range: %.2f", median)
	}

	if s1.Rank(2000) != 2000 {
		t.Fatalf("rank after merge incorrect")
	}
}

func TestKLL_IdempotentMerge(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)
	empty := newTestKLL(t, k)

	for i := 0; i < 1000; i++ {
		s.Insert(float64(i))
	}

	before := s.CDF().Query(0.5)

	if err := s.Merge(empty); err != nil {
		t.Fatalf("merge failed")
	}

	after := s.CDF().Query(0.5)
	if before != after {
		t.Fatalf("merge with empty changed result")
	}
}

// ======================
// Distribution Properties
// ======================

func TestKLL_CDF_Gaussian(t *testing.T) {
	k := 200
	n := 5000
	s := newTestKLL(t, k)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < n; i++ {
		s.Insert(rng.NormFloat64())
	}

	cdf := s.CDF()

	if math.Abs(cdf.Query(0.5)) > 0.15 {
		t.Fatalf("median of gaussian not ~0")
	}
	if math.Abs(cdf.Query(0.841)-1.0) > 0.2 {
		t.Fatalf("+1 sigma incorrect")
	}
	if math.Abs(cdf.Query(0.159)+1.0) > 0.2 {
		t.Fatalf("-1 sigma incorrect")
	}
}

func TestKLL_QuantileMonotonicity(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	for i := 0; i < 10_000; i++ {
		s.Insert(rand.Float64())
	}

	cdf := s.CDF()
	prev := math.Inf(-1)

	for _, p := range []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99} {
		v := cdf.Query(p)
		if v < prev {
			t.Fatalf("quantile monotonicity violated")
		}
		prev = v
	}
}

func TestKLL_ExtremeQuantiles(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	for i := 0; i < 100_000; i++ {
		s.Insert(rand.ExpFloat64())
	}

	cdf := s.CDF()
	if cdf.Query(0.999) < cdf.Query(0.99) {
		t.Fatalf("tail quantile ordering violated")
	}
}

// ======================
// Streaming Properties
// ======================

func TestKLL_OrderIndependence_ErrorBound(t *testing.T) {
	k := 200
	n := 10000
	data := make([]float64, n)
	for i := range data {
		data[i] = float64(i)
	}

	s1 := newTestKLL(t, k)
	s2 := newTestKLL(t, k)

	for _, v := range data {
		s1.Insert(v)
	}
	for i := len(data) - 1; i >= 0; i-- {
		s2.Insert(data[i])
	}

	sort.Float64s(data)
	trueMedian := data[n/2]

	q1 := s1.CDF().Query(0.5)
	q2 := s2.CDF().Query(0.5)

	err1 := math.Abs(q1-trueMedian) / trueMedian
	err2 := math.Abs(q2-trueMedian) / trueMedian

	t.Logf("median errors: forward=%.4f%% reverse=%.4f%%",
		err1*100, err2*100)

	if err1 > 0.02 || err2 > 0.02 {
		t.Fatalf("order dependence causes excessive error")
	}
}

func TestKLL_QueryStability(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	for i := 0; i < 5000; i++ {
		s.Insert(float64(i))
	}

	cdf := s.CDF()
	v1 := cdf.Query(0.9)
	v2 := cdf.Query(0.9)
	v3 := cdf.Query(0.9)

	if v1 != v2 || v2 != v3 {
		t.Fatalf("query not stable")
	}
}

// ======================
// Memory & Space Guarantees
// ======================

func TestKLL_RetainedItemsVsN(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	N := 50_000
	for i := 0; i < N; i++ {
		s.Insert(float64(i))
	}

	retained := s.GetRetainedItems()
	t.Logf("Retained=%d Total=%d", retained, N)

	if retained > 10*k {
		t.Fatalf("too many retained items")
	}
}

func TestKLL_MemoryBound(t *testing.T) {
	k := 200
	s := newTestKLL(t, k)

	N := 1_000_000
	for i := 0; i < N; i++ {
		s.Insert(float64(i))
	}

	memKB := s.GetMemoryBytes() / 1024
	t.Logf("Memory usage: %.2f KB for N=%d", memKB, N)

	if memKB > float64(k)*10 {
		t.Fatalf("memory usage too large")
	}
}
