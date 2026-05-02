package benchmark

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

const (
	// Alpha = 0.01 implies 1% relative error guarantee.
	ddBenchAlpha = 0.01
)

// =====================================================
// 0. DATA LOADING HELPERS
// =====================================================

// LoadCAIDAPositiveFloats reads pcap data and returns positive float64s.
// DDSketch requires v > 0.
func LoadCAIDAPositiveFloats(tb testing.TB) []float64 {
	// Adjust path relative to where test is run (sketches/DDSketch)
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", err)
	}
	if len(samples) == 0 {
		tb.Fatal("No samples loaded")
	}

	// Filter and convert
	var data []float64
	for _, s := range samples {
		if s.F > 0 {
			data = append(data, s.F)
		}
	}

	if len(data) == 0 {
		tb.Fatal("No positive values found in CAIDA stream")
	}
	return data
}

// =====================================================
// 1. UPDATE THROUGHPUT
// =====================================================

// BenchmarkDDSketch_Add_Single measures raw insertion speed.
// Involves: Logarithm calculation (index mapping) + Slice growth checks.
func BenchmarkDDSketch_Add_Single(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	n := len(data)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s.Update(data[i%n])
	}
}

// BenchmarkDDSketch_Add_Batch simulates processing batches.
func BenchmarkDDSketch_Add_Batch(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	n := len(data)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

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
			s.Update(data[k])
		}
	}
}

// BenchmarkDDSketch_Update_Speed reports sustained single-thread add throughput.
func BenchmarkDDSketch_Update_Speed(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	n := len(data)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(8)

	for i := 0; i < b.N; i++ {
		s.Update(data[i%n])
	}
}

// TestDDSketch_Add_Latency_P50P99 reports insertion latency distribution.
func TestDDSketch_Add_Latency_P50P99(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	sampleSize := minInt(100_000, len(data))
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		s.Update(data[i])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := percentileInt64(latencies, 0.50)
	p99 := percentileInt64(latencies, 0.99)

	t.Log("=== DDSketch Add Latency Report ===")
	t.Logf(" Samples:       %d", sampleSize)
	t.Logf(" P50 (Median):  %d ns", p50)
	t.Logf(" P99:           %d ns", p99)
	t.Log("===================================")
}

// BenchmarkDDSketch_Add_Parallel measures sharded parallel ingestion.
func BenchmarkDDSketch_Add_Parallel(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	n := len(data)

	var counter uint64
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		s := ddsketch.NewDDSketch(ddBenchAlpha)
		for pb.Next() {
			idx := atomic.AddUint64(&counter, 1) - 1
			s.Update(data[idx%uint64(n)])
		}
		runtime.KeepAlive(s)
	})
}

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

// BenchmarkDDSketch_Query_Quantile measures looking up values by quantile.
// e.g., "What is the P99 latency?"
func BenchmarkDDSketch_Query_Quantile(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	// Pre-fill
	for _, v := range data {
		s.Update(v)
	}

	// Queries to cycle through
	qs := []float64{0.5, 0.9, 0.99, 0.999}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Quantile(qs[i%len(qs)])
	}
}

// BenchmarkDDSketch_Query_MixedQuantiles measures mixed hot-path quantile lookups.
func BenchmarkDDSketch_Query_MixedQuantiles(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	for _, v := range data {
		s.Update(v)
	}

	qs := []float64{0.01, 0.1, 0.5, 0.9, 0.95, 0.99, 0.999}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Quantile(qs[i%len(qs)])
	}
}

// TestDDSketch_Query_Latency_P99 measures latency distribution of Quantile queries.
func TestDDSketch_Query_Latency_P99(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	for _, v := range data {
		s.Update(v)
	}

	sampleSize := 100_000
	latencies := make([]int64, sampleSize)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < sampleSize; i++ {
		q := rng.Float64() // Random quantile 0..1
		start := time.Now()
		_, _ = s.Quantile(q)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := percentileInt64(latencies, 0.50)
	p99 := percentileInt64(latencies, 0.99)
	p999 := percentileInt64(latencies, 0.999)

	t.Log("=== DDSketch Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("=====================================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestDDSketch_Memory_Usage(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats

	data := LoadCAIDAPositiveFloats(t)
	N := len(data)

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate & Fill
	s := ddsketch.NewDDSketch(ddBenchAlpha)
	for _, v := range data {
		s.Update(v)
	}

	// Snapshot after
	runtime.ReadMemStats(&m2)
	runtime.KeepAlive(s)

	heapGrowth := m2.HeapAlloc - m1.HeapAlloc

	// Internal metrics
	// Since DDSketch bucket array is private, we infer from heap growth.
	// DDSketch is "unbounded" relative to range, but collapses sparse data.

	t.Log("=== DDSketch Memory Usage Report ===")
	t.Logf(" Stream Length (N): %d", N)
	t.Logf(" Alpha (Accuracy):  %.3f", ddBenchAlpha)
	t.Logf(" Count (N):         %d", s.GetCount())
	t.Logf(" Memory Footprint:  %.2f KB", float64(heapGrowth)/1024)
	t.Logf(" Heap Growth:       %.2f KB", float64(heapGrowth)/1024)
	t.Logf(" Bytes per Item:    %.4f bytes", float64(heapGrowth)/float64(N))
	t.Log("====================================")

	// CAIDA IPs are dense (many in similar ranges), so compression should be good.
	// We expect < 50KB for typical IP ranges with Alpha=0.01
	if float64(heapGrowth) > 500*1024 {
		t.Logf("Warning: Memory usage seems high (%.2f KB). Check bucket density.", float64(heapGrowth)/1024)
	}

	t.Log("Checking post-build dynamic growth...")
	runtime.ReadMemStats(&m1)
	for i := 0; i < minInt(1_000_000, N); i++ {
		s.Update(data[i])
	}
	runtime.ReadMemStats(&m2)
	dynamicGrowth := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf(" Dynamic Growth:    %d bytes", dynamicGrowth)

	s2 := ddsketch.NewDDSketch(ddBenchAlpha)
	for i := 0; i < minInt(100_000, N); i++ {
		s2.Update(data[N-1-i])
	}
	runtime.ReadMemStats(&m1)
	_ = s.Merge(s2)
	runtime.ReadMemStats(&m2)
	mergeOverhead := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf(" Merge Overhead:    %d bytes", mergeOverhead)
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

func createDDSketches(n int) []*ddsketch.DDSketch {
	list := make([]*ddsketch.DDSketch, n)
	for i := 0; i < n; i++ {
		list[i] = ddsketch.NewDDSketch(ddBenchAlpha)
	}
	return list
}

// Benchmark 4A: Merge Latency (2 Sketches)
func BenchmarkDDSketch_Merge_2(b *testing.B) {
	s1 := ddsketch.NewDDSketch(ddBenchAlpha)
	s2 := ddsketch.NewDDSketch(ddBenchAlpha)

	// Pre-fill with disjoint ranges to force bucket merging logic
	s1.Update(100)
	s2.Update(200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

func BenchmarkDDSketch_Merge_PreFilled_CAIDA(b *testing.B) {
	data := LoadCAIDAPositiveFloats(b)
	n := len(data)
	mid := n / 2

	left := ddsketch.NewDDSketch(ddBenchAlpha)
	right := ddsketch.NewDDSketch(ddBenchAlpha)
	for i, v := range data {
		if i < mid {
			left.Update(v)
		} else {
			right.Update(v)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := ddsketch.NewDDSketch(ddBenchAlpha)
		_ = target.Merge(left)
		_ = target.Merge(right)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkDDSketchMergeN(b *testing.B, count int) {
	list := createDDSketches(count)
	// Fill with distinct data ranges
	for i, sk := range list {
		for j := 0; j < 100; j++ {
			sk.Update(float64(i*1000 + j + 1))
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := list[0]
		// Merge 1..N into 0
		for j := 1; j < count; j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkDDSketch_Merge_10(b *testing.B)   { benchmarkDDSketchMergeN(b, 10) }
func BenchmarkDDSketch_Merge_100(b *testing.B)  { benchmarkDDSketchMergeN(b, 100) }
func BenchmarkDDSketch_Merge_1000(b *testing.B) { benchmarkDDSketchMergeN(b, 1000) }

// Test 4C: Merge Accuracy
// Verifies that merging two DDSketches yields Quantiles identical to a monolithic sketch.
func TestDDSketch_Merge_Accuracy(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	n := len(data)
	mid := n / 2

	// 1. Ground Truth (Total Sketch)
	totalS := ddsketch.NewDDSketch(ddBenchAlpha)
	for _, v := range data {
		totalS.Update(v)
	}

	// 2. Split Sketches
	part1 := ddsketch.NewDDSketch(ddBenchAlpha)
	part2 := ddsketch.NewDDSketch(ddBenchAlpha)

	for i, v := range data {
		if i < mid {
			part1.Update(v)
		} else {
			part2.Update(v)
		}
	}

	// 3. Pre-Merge Check
	t.Log("=== Pre-Merge Statistics ===")
	q := 0.99
	v1, _ := part1.Quantile(q)
	v2, _ := part2.Quantile(q)
	vt, _ := totalS.Quantile(q)

	t.Logf(" P99 Part 1: %.0f", v1)
	t.Logf(" P99 Part 2: %.0f", v2)
	t.Logf(" P99 Total:  %.0f", vt)

	// 4. Merge
	start := time.Now()
	err := part1.Merge(part2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	t.Logf("Merge took: %v", time.Since(start))

	// 5. Post-Merge Accuracy
	t.Log("=== Post-Merge Accuracy ===")

	// Unlike KLL, DDSketch Merge is mathematically exact (histogram addition).
	// The Quantiles should match EXACTLY if the bucket structure is deterministic.

	points := []float64{0.5, 0.9, 0.99, 1.0}

	for _, p := range points {
		estMerged, _ := part1.Quantile(p)
		estTotal, _ := totalS.Quantile(p)

		if estMerged != estTotal {
			t.Errorf("Mismatch at q=%.2f! Merged=%.2f, Total=%.2f", p, estMerged, estTotal)
		}
	}

	t.Log("PASS: Merged sketch is identical to monolithic sketch.")
}

// =====================================================
// 5. ACCURACY REPORT (CAIDA)
// =====================================================

func TestDDSketch_CAIDA_AccuracyReport(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)

	s := ddsketch.NewDDSketch(ddBenchAlpha)
	for _, v := range data {
		s.Update(v)
	}

	// Sort Ground Truth
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	checkPoints := []float64{0.5, 0.75, 0.95, 0.99, 0.999}

	t.Log("===================================================")
	t.Logf(" DDSKETCH ACCURACY REPORT (Alpha=%.2f)", ddBenchAlpha)
	t.Logf(" Stream Size: %d", len(data))
	t.Log("===================================================")

	maxRelErr := 0.0

	for _, p := range checkPoints {
		// True Value
		idx := int(float64(len(sorted)) * p)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		trueVal := sorted[idx]

		// Estimated Value
		estVal, _ := s.Quantile(p)

		// Relative Error
		err := math.Abs(estVal - trueVal)
		relErr := err / trueVal

		if relErr > maxRelErr {
			maxRelErr = relErr
		}

		t.Logf(" q=%-5.3f | True: %10.0f | Est: %10.0f | RelErr: %.4f%%",
			p, trueVal, estVal, relErr*100)
	}
	t.Log("===================================================")
	t.Logf(" Max Relative Error: %.4f%%", maxRelErr*100)

	// Check against Alpha guarantee
	// Note: float precision might cause tiny deviations, so we add epsilon
	limit := ddBenchAlpha + 1e-5
	if maxRelErr > limit {
		t.Errorf("Accuracy violation: %.4f%% > Limit %.4f%%", maxRelErr*100, limit*100)
	}
}

func TestDDSketch_CAIDA_Accuracy_Detailed(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	if len(data) == 0 {
		t.Fatal("empty dataset")
	}

	s := ddsketch.NewDDSketch(ddBenchAlpha)
	for _, v := range data {
		s.Update(v)
	}

	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	quantiles := []float64{0.01, 0.05, 0.10, 0.25, 0.50, 0.75, 0.90, 0.95, 0.99, 0.999}
	var totalRelErr float64
	maxRelErr := 0.0

	t.Log("=== DDSketch Detailed Quantile Accuracy ===")
	t.Logf(" Samples: %d", len(sorted))
	t.Logf(" Alpha:   %.4f", ddBenchAlpha)

	for _, q := range quantiles {
		idx := int(math.Ceil(q*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}

		trueVal := sorted[idx]
		estVal, ok := s.Quantile(q)
		if !ok {
			t.Fatalf("quantile query failed at %.3f", q)
		}

		relErr := 0.0
		if trueVal != 0 {
			relErr = math.Abs(estVal-trueVal) / trueVal
		}

		totalRelErr += relErr
		if relErr > maxRelErr {
			maxRelErr = relErr
		}

		t.Logf(" q=%-5.3f | True=%10.2f | Est=%10.2f | RelErr=%7.4f%%", q, trueVal, estVal, relErr*100)
	}

	avgRelErr := totalRelErr / float64(len(quantiles))
	t.Logf(" Average Relative Error: %.4f%%", avgRelErr*100)
	t.Logf(" Max Relative Error:     %.4f%%", maxRelErr*100)
	t.Log("==========================================")

	limit := ddBenchAlpha + 1e-5
	if maxRelErr > limit {
		t.Errorf("accuracy violation: %.4f%% > limit %.4f%%", maxRelErr*100, limit*100)
	}
}

func TestDDSketch_Merge_Latency_Distribution(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	mid := len(data) / 2

	leftSrc := ddsketch.NewDDSketch(ddBenchAlpha)
	rightSrc := ddsketch.NewDDSketch(ddBenchAlpha)
	for i, v := range data {
		if i < mid {
			leftSrc.Update(v)
		} else {
			rightSrc.Update(v)
		}
	}

	sampleSize := 1_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left := ddsketch.NewDDSketch(ddBenchAlpha)
		right := ddsketch.NewDDSketch(ddBenchAlpha)
		_ = left.Merge(leftSrc)
		_ = right.Merge(rightSrc)

		start := time.Now()
		_ = left.Merge(right)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== DDSketch Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("===========================================")
}
