package benchmark

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	kll "github.com/approx-telemetry/sketchlib-go/sketches/KLL"
)

// =====================================================
// 1. ACCURACY REPORT (NOT A MICROBENCHMARK)
// =====================================================

func TestKLL_AccuracyReport(t *testing.T) {
	k := 200
	totalItems := 100_000

	s, err := kll.NewKLLSketch(k)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	rng := rand.New(rand.NewSource(42))
	values := make([]float64, 0, totalItems)

	for i := 0; i < totalItems; i++ {
		v := rng.Float64() * 1000
		values = append(values, v)
		s.Insert(v)
	}

	// Ground truth
	sort.Float64s(values)

	quantiles := []float64{0.5, 0.9, 0.99}
	var totalErr float64

	for _, q := range quantiles {
		idx := int(q * float64(len(values)))
		trueV := values[idx]

		estV := s.CDF().Query(q)

		relErr := math.Abs(estV-trueV) / math.Abs(trueV)
		totalErr += relErr

		t.Logf("[Quantile] q=%.2f true=%.4f est=%.4f rel_err=%.4f",
			q, trueV, estV, relErr)
	}

	avgErr := totalErr / float64(len(quantiles)) * 100

	t.Log("===================================================")
	t.Logf(" KLL ACCURACY REPORT (N=%d, k=%d)", totalItems, k)
	t.Log("===================================================")
	t.Logf(" Avg Relative Quantile Error: %.4f%%", avgErr)
	t.Log("===================================================")

	if avgErr > 5.0 {
		t.Errorf("quantile error too high")
	}
}

//
// ==========================
// 2. MICRO BENCHMARKS
// ==========================
//

const (
	kllBenchK = 200
	kllBenchN = 1 << 20
)

// Pre-generate values
func makeKLLValues(n int) []float64 {
	vals := make([]float64, n)
	for i := 0; i < n; i++ {
		vals[i] = float64(i)
	}
	return vals
}

// ------------------------------------------
// Benchmark 1: Insert (fast path)
// ------------------------------------------
func BenchmarkKLL_Insert(b *testing.B) {
	s, _ := kll.NewKLLSketch(kllBenchK)
	values := makeKLLValues(kllBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s.Insert(values[i&(kllBenchN-1)])
	}
}

// ------------------------------------------
// Benchmark 2: Rank query
// ------------------------------------------
func BenchmarkKLL_Rank(b *testing.B) {
	s, _ := kll.NewKLLSketch(kllBenchK)
	values := makeKLLValues(100_000)

	for _, v := range values {
		s.Insert(v)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = s.Rank(values[i%len(values)])
	}
}

// ------------------------------------------
// Benchmark 3: Quantile query
// ------------------------------------------
func BenchmarkKLL_QuantileQuery(b *testing.B) {
	s, _ := kll.NewKLLSketch(kllBenchK)

	for i := 0; i < 100_000; i++ {
		s.Insert(float64(i))
	}

	cdf := s.CDF()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cdf.Query(0.99)
	}
}

// ------------------------------------------
// Benchmark 4: Merge (distributed scenario)
// ------------------------------------------
func BenchmarkKLL_Merge(b *testing.B) {
	s1, _ := kll.NewKLLSketch(kllBenchK)
	s2, _ := kll.NewKLLSketch(kllBenchK)

	for i := 0; i < 100_000; i++ {
		s1.Insert(float64(i))
		s2.Insert(float64(i))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}
