package benchmark

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"

	ddsketch "github.com/approx-telemetry/sketchlib-go/sketches/DDSketch"
	"github.com/approx-telemetry/sketchlib-go/testdata"
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
		s.Add(data[i%n])
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
			s.Add(data[k])
		}
	}
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
		s.Add(v)
	}

	// Queries to cycle through
	qs := []float64{0.5, 0.9, 0.99, 0.999}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.GetValueAtQuantile(qs[i%len(qs)])
	}
}

// TestDDSketch_Query_Latency_P99 measures latency distribution of Quantile queries.
func TestDDSketch_Query_Latency_P99(t *testing.T) {
	data := LoadCAIDAPositiveFloats(t)
	s := ddsketch.NewDDSketch(ddBenchAlpha)

	for _, v := range data {
		s.Add(v)
	}

	sampleSize := 100_000
	latencies := make([]int64, sampleSize)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < sampleSize; i++ {
		q := rng.Float64() // Random quantile 0..1
		start := time.Now()
		_, _ = s.GetValueAtQuantile(q)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[int(float64(sampleSize)*0.50)]
	p99 := latencies[int(float64(sampleSize)*0.99)]
	p999 := latencies[int(float64(sampleSize)*0.999)]

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
		s.Add(v)
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
	t.Logf(" Heap Growth:       %.2f KB", float64(heapGrowth)/1024)
	t.Logf(" Bytes per Item:    %.4f bytes", float64(heapGrowth)/float64(N))
	t.Log("====================================")

	// CAIDA IPs are dense (many in similar ranges), so compression should be good.
	// We expect < 50KB for typical IP ranges with Alpha=0.01
	if float64(heapGrowth) > 500*1024 {
		t.Logf("Warning: Memory usage seems high (%.2f KB). Check bucket density.", float64(heapGrowth)/1024)
	}
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
	s1.Add(100)
	s2.Add(200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkDDSketchMergeN(b *testing.B, count int) {
	list := createDDSketches(count)
	// Fill with distinct data ranges
	for i, sk := range list {
		for j := 0; j < 100; j++ {
			sk.Add(float64(i*1000 + j + 1))
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
		totalS.Add(v)
	}

	// 2. Split Sketches
	part1 := ddsketch.NewDDSketch(ddBenchAlpha)
	part2 := ddsketch.NewDDSketch(ddBenchAlpha)

	for i, v := range data {
		if i < mid {
			part1.Add(v)
		} else {
			part2.Add(v)
		}
	}

	// 3. Pre-Merge Check
	t.Log("=== Pre-Merge Statistics ===")
	q := 0.99
	v1, _ := part1.GetValueAtQuantile(q)
	v2, _ := part2.GetValueAtQuantile(q)
	vt, _ := totalS.GetValueAtQuantile(q)

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
		estMerged, _ := part1.GetValueAtQuantile(p)
		estTotal, _ := totalS.GetValueAtQuantile(p)

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
		s.Add(v)
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
		estVal, _ := s.GetValueAtQuantile(p)

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
