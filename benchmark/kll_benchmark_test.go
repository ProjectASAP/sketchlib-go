package benchmark

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"

	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

const (
	// K=200 is a standard default for KLL, offering a good balance of accuracy vs size.
	// Max Rank Error approx 1.0/k to 2.0/k (~0.5% - 1.0%)
	kllBenchK = 200
)

// =====================================================
// 0. DATA LOADING HELPERS
// =====================================================

// LoadCAIDAFloats reads pcap data and returns IP addresses as float64s.
// KLL treats these floats as the distribution to measure.
func LoadCAIDAFloats(tb testing.TB) []float64 {
	// Adjust path relative to where test is run (sketches/kll)
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", err)
	}
	if len(samples) == 0 {
		tb.Fatal("No samples loaded")
	}

	data := make([]float64, len(samples))
	for i, s := range samples {
		data[i] = s.F
	}
	return data
}

// =====================================================
// 1. UPDATE THROUGHPUT
// =====================================================

// BenchmarkKLL_Insert_Single measures raw insertion speed.
// KLL insertion involves compaction cycles, so latency will spike periodically.
func BenchmarkKLL_Insert_Single(b *testing.B) {
	data := LoadCAIDAFloats(b)
	n := len(data)
	s, _ := kll.NewKLLSketch(kllBenchK)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s.Insert(data[i%n])
	}
}

// BenchmarkKLL_Insert_Batch simulates processing batches.
func BenchmarkKLL_Insert_Batch(b *testing.B) {
	data := LoadCAIDAFloats(b)
	n := len(data)
	s, _ := kll.NewKLLSketch(kllBenchK)

	batchSize := 1000

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		start := (i * batchSize) % n
		end := start + batchSize
		if end > n {
			end = n
		}

		for k := start; k < end; k++ {
			s.Insert(data[k])
		}
	}
}

func TestKLL_Insert_Latency_P50P99(t *testing.T) {
	data := LoadCAIDAFloats(t)
	s, _ := kll.NewKLLSketch(kllBenchK)

	sampleSize := benchMinInt(100_000, len(data))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		s.Insert(data[i])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== KLL Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=================================")
}

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

// BenchmarkKLL_Query_Quantile measures looking up values by rank (CDF inverse).
// e.g., "What value is at the 99th percentile?"
func BenchmarkKLL_Query_Quantile(b *testing.B) {
	data := LoadCAIDAFloats(b)
	s, _ := kll.NewKLLSketch(kllBenchK)

	// Pre-fill
	for _, v := range data {
		s.Insert(v)
	}

	// Queries to cycle through
	qs := []float64{0.1, 0.5, 0.9, 0.99}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Quantile(qs[i%len(qs)])
	}
}

// BenchmarkKLL_Query_Rank measures looking up rank by value (CDF).
// e.g., "What is the percentile of this IP address?"
func BenchmarkKLL_Query_Rank(b *testing.B) {
	data := LoadCAIDAFloats(b)
	n := len(data)
	s, _ := kll.NewKLLSketch(kllBenchK)

	for _, v := range data {
		s.Insert(v)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Rank(data[i%n])
	}
}

// TestKLL_Query_Latency_P99 measures the latency distribution of Quantile queries.
func TestKLL_Query_Latency_P99(t *testing.T) {
	data := LoadCAIDAFloats(t)
	s, _ := kll.NewKLLSketch(kllBenchK)

	for _, v := range data {
		s.Insert(v)
	}

	sampleSize := 100_000
	latencies := make([]int64, sampleSize)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < sampleSize; i++ {
		q := rng.Float64() // Random quantile 0..1
		start := time.Now()
		_ = s.Quantile(q)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[int(float64(sampleSize)*0.50)]
	p99 := latencies[int(float64(sampleSize)*0.99)]
	p999 := latencies[int(float64(sampleSize)*0.999)]

	t.Log("=== KLL Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("================================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestKLL_Memory_Usage(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats

	data := LoadCAIDAFloats(t)
	N := len(data)

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate & Fill
	s, _ := kll.NewKLLSketch(kllBenchK)
	for _, v := range data {
		s.Insert(v)
	}

	// Snapshot after
	runtime.ReadMemStats(&m2)
	runtime.KeepAlive(s)

	heapGrowth := m2.HeapAlloc - m1.HeapAlloc
	internalEst := s.GetMemoryBytes()

	t.Log("=== KLL Memory Usage Report ===")
	t.Logf(" Stream Length (N): %d", N)
	t.Logf(" K Parameter:       %d", kllBenchK)
	t.Logf(" Retained Items:    %d", s.GetRetainedItems())
	t.Logf(" Est. Internal Mem: %.2f KB", internalEst/1024)
	t.Logf(" Heap Growth:       %.2f KB", float64(heapGrowth)/1024)
	t.Logf(" Bytes per Item:    %.2f bytes", float64(heapGrowth)/float64(N)) // Should be tiny < 1 byte
	t.Log("===============================")

	if s.GetRetainedItems() > kllBenchK*50 {
		t.Errorf("Retained items %d exceeds expected bound for k=%d", s.GetRetainedItems(), kllBenchK)
	}
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

func createKLLSketches(n int) []*kll.KLLSketch {
	list := make([]*kll.KLLSketch, n)
	for i := 0; i < n; i++ {
		list[i], _ = kll.NewKLLSketch(kllBenchK)
	}
	return list
}

// Benchmark 4A: Merge Latency (2 Sketches)
func BenchmarkKLL_Merge_2(b *testing.B) {
	s1, _ := kll.NewKLLSketch(kllBenchK)
	s2, _ := kll.NewKLLSketch(kllBenchK)

	// Pre-fill slightly to ensure compaction logic runs
	for i := 0; i < 1000; i++ {
		s1.Insert(float64(i))
		s2.Insert(float64(i * 2))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkKLLMergeN(b *testing.B, count int) {
	list := createKLLSketches(count)
	// Fill with distinct data ranges
	for i, sk := range list {
		for j := 0; j < 100; j++ {
			sk.Insert(float64(i*100 + j))
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := list[0]
		// We merge 1..N into 0
		for j := 1; j < count; j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkKLL_Merge_10(b *testing.B)   { benchmarkKLLMergeN(b, 10) }
func BenchmarkKLL_Merge_100(b *testing.B)  { benchmarkKLLMergeN(b, 100) }
func BenchmarkKLL_Merge_1000(b *testing.B) { benchmarkKLLMergeN(b, 1000) }

// Test 4C: Merge Accuracy
// Verifies that merging partial sketches yields a rank error similar to a monolithic sketch.
func TestKLL_Merge_Accuracy(t *testing.T) {
	data := LoadCAIDAFloats(t)
	n := len(data)
	mid := n / 2

	// 1. Ground Truth (Total Sketch)
	totalS, _ := kll.NewKLLSketch(kllBenchK)
	for _, v := range data {
		totalS.Insert(v)
	}

	// 2. Split Sketches
	part1, _ := kll.NewKLLSketch(kllBenchK)
	part2, _ := kll.NewKLLSketch(kllBenchK)

	for i, v := range data {
		if i < mid {
			part1.Insert(v)
		} else {
			part2.Insert(v)
		}
	}

	// 3. Pre-Merge Check
	t.Log("=== Pre-Merge Statistics ===")
	q := 0.5 // Check Median
	v1 := part1.Quantile(q)
	v2 := part2.Quantile(q)
	vt := totalS.Quantile(q)

	t.Logf(" Median Part 1: %.0f", v1)
	t.Logf(" Median Part 2: %.0f", v2)
	t.Logf(" Median Total:  %.0f", vt)

	// 4. Merge
	start := time.Now()
	err := part1.Merge(part2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	t.Logf("Merge took: %v", time.Since(start))

	// 5. Post-Merge Accuracy (Rank Error)
	t.Log("=== Post-Merge Accuracy ===")

	// Check quantiles 0.1 to 0.9
	points := []float64{0.1, 0.5, 0.9, 0.99}
	var maxDiff float64

	// Range of values to normalize error
	minVal := totalS.Quantile(0.0)
	maxVal := totalS.Quantile(1.0)
	valueRange := maxVal - minVal

	for _, p := range points {
		estMerged := part1.Quantile(p)
		estTotal := totalS.Quantile(p)

		diff := math.Abs(estMerged - estTotal)
		normDiff := diff / valueRange // Normalized difference

		if normDiff > maxDiff {
			maxDiff = normDiff
		}

		t.Logf(" p=%.2f | Merged: %.0f | Total: %.0f | Diff: %.0f (Norm: %.4f)",
			p, estMerged, estTotal, diff, normDiff)
	}

	// KLL Merge is approximate, but should stay within reasonable bounds.
	// 5% normalized difference is a safe upper bound for K=200 on this dataset.
	if maxDiff > 0.05 {
		t.Errorf("Merge resulted in significant deviation (%.2f%%)", maxDiff*100)
	} else {
		t.Logf("PASS: Merged sketch is consistent with monolithic sketch (Max Diff: %.2f%%)", maxDiff*100)
	}
}

// =====================================================
// 5. ACCURACY REPORT (CAIDA)
// =====================================================

func TestKLL_CAIDA_AccuracyReport(t *testing.T) {
	data := LoadCAIDAFloats(t)

	s, _ := kll.NewKLLSketch(kllBenchK)
	for _, v := range data {
		s.Insert(v)
	}

	// Sort Ground Truth
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	checkPoints := []float64{0.01, 0.05, 0.25, 0.5, 0.75, 0.95, 0.99}

	t.Log("===================================================")
	t.Logf(" KLL ACCURACY REPORT (K=%d, N=%d)", kllBenchK, len(data))
	t.Log("===================================================")

	maxRankErr := 0.0

	for _, p := range checkPoints {
		// True Rank
		idx := int(float64(len(sorted)) * p)
		val := sorted[idx]
		trueRank := float64(idx) / float64(len(sorted))

		// Estimated Rank
		estCount := s.Rank(val)
		estRank := float64(estCount) / float64(len(sorted))

		err := math.Abs(estRank - trueRank)
		if err > maxRankErr {
			maxRankErr = err
		}

		t.Logf(" p=%-4.2f | Val: %10.0f | TrueRank: %.4f | EstRank: %.4f | Err: %.4f%%",
			p, val, trueRank, estRank, err*100)
	}
	t.Log("===================================================")
	t.Logf(" Max Rank Error: %.4f%%", maxRankErr*100)

	// Theoretical bound is ~1/K. For K=200, ~0.5%. We allow 1.5% for noise.
	if maxRankErr > 0.015 {
		t.Errorf("Accuracy too low: Max Rank Error %.4f%% > 1.5%%", maxRankErr*100)
	}
}

func TestKLL_Merge_Latency_Distribution(t *testing.T) {
	data := LoadCAIDAFloats(t)
	mid := len(data) / 2

	leftSrc, _ := kll.NewKLLSketch(kllBenchK)
	rightSrc, _ := kll.NewKLLSketch(kllBenchK)
	for i, v := range data {
		if i < mid {
			leftSrc.Insert(v)
		} else {
			rightSrc.Insert(v)
		}
	}

	sampleSize := 1_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left, _ := kll.NewKLLSketch(kllBenchK)
		right, _ := kll.NewKLLSketch(kllBenchK)
		_ = left.Merge(leftSrc)
		_ = right.Merge(rightSrc)

		start := time.Now()
		_ = left.Merge(right)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== KLL Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("======================================")
}
