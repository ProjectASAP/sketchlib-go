package benchmark

import (
	"math"
	"runtime"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// =====================================================
// 0. DATA LOADING HELPERS
// =====================================================

// LoadCAIDAHashes reads pcap data and returns pre-computed hashes.
// HLL needs hashes for the fast path.
func LoadCAIDAHashes(tb testing.TB) []uint64 {
	// Adjust path relative to where test is run (sketches/HLL)
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", err)
	}
	if len(samples) == 0 {
		tb.Fatal("No samples loaded")
	}

	hashes := make([]uint64, len(samples))
	for i, s := range samples {
		// Use the float64 value (IP) -> Bytes -> Hash
		buf := common.Float64ToBytes(s.F)
		hashes[i] = common.Hash64(buf)
	}
	return hashes
}

// =====================================================
// 1. UPDATE THROUGHPUT
// =====================================================

// BenchmarkHLL_Insert_Single measures the Fast Path (InsertWithHash).
// This is the most critical path for high-throughput systems.
func BenchmarkHLL_Insert_Single(b *testing.B) {
	hashes := LoadCAIDAHashes(b)
	n := len(hashes)
	h := hll.NewHyperLogLog()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		h.InsertWithHash(hashes[i%n])
	}
}

// BenchmarkHLL_Insert_Batch simulates processing batches.
// HLL is extremely fast, so loop overhead matters.
func BenchmarkHLL_Insert_Batch(b *testing.B) {
	hashes := LoadCAIDAHashes(b)
	n := len(hashes)
	h := hll.NewHyperLogLog()

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
			h.InsertWithHash(hashes[k])
		}
	}
}

func TestHLL_Insert_Latency_P50P99(t *testing.T) {
	hashes := LoadCAIDAHashes(t)
	h := hll.NewHyperLogLog()

	sampleSize := benchMinInt(100_000, len(hashes))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		h.InsertWithHash(hashes[i])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== HLL Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=================================")
}

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

// BenchmarkHLL_Query_Estimate measures the cost of calculating cardinality.
// This involves iterating over 16k registers and math operations (harmonic mean).
func BenchmarkHLL_Query_Estimate(b *testing.B) {
	hashes := LoadCAIDAHashes(b)
	h := hll.NewHyperLogLog()

	// Pre-fill with data to ensure non-trivial estimation
	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Estimate()
	}
}

// TestHLL_Estimate_Latency_P99 measures the latency distribution of Estimate().
func TestHLL_Estimate_Latency_P99(t *testing.T) {
	hashes := LoadCAIDAHashes(t)
	h := hll.NewHyperLogLog()

	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}

	sampleSize := 10_000 // Estimate is slower than insert, so fewer samples
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_ = h.Estimate()
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[int(float64(sampleSize)*0.50)]
	p99 := latencies[int(float64(sampleSize)*0.99)]
	p999 := latencies[int(float64(sampleSize)*0.999)]

	t.Log("=== HLL Estimate Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("===================================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestHLL_Memory_Usage(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats

	hashes := LoadCAIDAHashes(t)
	N := len(hashes)

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate & Fill
	h := hll.NewHyperLogLog()
	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}

	// Snapshot after
	runtime.ReadMemStats(&m2)
	runtime.KeepAlive(h)

	// HLL Memory is FIXED.
	// 14-bit precision = 2^14 registers = 16384 bytes.
	const TheoreticalSize = 16384

	heapGrowth := m2.HeapAlloc - m1.HeapAlloc

	t.Log("=== HLL Memory Usage Report ===")
	t.Logf(" Stream Length (N): %d", N)
	t.Logf(" Theoretical Size:  %d bytes", TheoreticalSize)
	t.Logf(" Heap Growth:       %d bytes", heapGrowth)
	t.Log("===============================")

	// Note: Heap growth might be slightly larger due to struct padding or test overhead,
	// but it should NOT grow with N.
	if heapGrowth > TheoreticalSize*2 {
		t.Logf("Warning: Heap growth (%d) significantly larger than theoretical (%d). Check allocations.", heapGrowth, TheoreticalSize)
	}
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

func createHLLSketches(n int) []*hll.HyperLogLog {
	list := make([]*hll.HyperLogLog, n)
	for i := 0; i < n; i++ {
		list[i] = hll.NewHyperLogLog()
	}
	return list
}

// Benchmark 4A: Merge Latency (2 Sketches)
func BenchmarkHLL_Merge_2(b *testing.B) {
	h1 := hll.NewHyperLogLog()
	h2 := hll.NewHyperLogLog()

	// Pre-fill with distinct data
	h1.InsertWithHash(1)
	h2.InsertWithHash(2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h1.Merge(h2)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkHLLMergeN(b *testing.B, count int) {
	list := createHLLSketches(count)
	// Fill with distinct data ranges
	for i, h := range list {
		for j := 0; j < 100; j++ {
			// Use deterministic hashing for consistent register population
			val := uint64(i*100 + j)
			// Simple mix to spread bits
			hash := val * 0x517cc1b727220a95
			h.InsertWithHash(hash)
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

func BenchmarkHLL_Merge_10(b *testing.B)   { benchmarkHLLMergeN(b, 10) }
func BenchmarkHLL_Merge_100(b *testing.B)  { benchmarkHLLMergeN(b, 100) }
func BenchmarkHLL_Merge_1000(b *testing.B) { benchmarkHLLMergeN(b, 1000) }

// Test 4C: Merge Accuracy
// Verifies that merging two HLLs is EXACTLY equivalent to a single HLL (Lossless Merge).
func TestHLL_Merge_Accuracy(t *testing.T) {
	hashes := LoadCAIDAHashes(t)
	n := len(hashes)
	mid := n / 2

	// 1. Ground Truth (Total Sketch)
	totalH := hll.NewHyperLogLog()
	for _, h := range hashes {
		totalH.InsertWithHash(h)
	}

	// 2. Split Sketches
	part1 := hll.NewHyperLogLog()
	part2 := hll.NewHyperLogLog()

	for i, h := range hashes {
		if i < mid {
			part1.InsertWithHash(h)
		} else {
			part2.InsertWithHash(h)
		}
	}

	// 3. Pre-Merge Check
	t.Log("=== Pre-Merge Statistics ===")
	est1 := part1.Estimate()
	est2 := part2.Estimate()
	estTot := totalH.Estimate()

	t.Logf(" Est Part 1: %d", est1)
	t.Logf(" Est Part 2: %d", est2)
	t.Logf(" Est Total:  %d", estTot)

	// 4. Merge
	start := time.Now()
	err := part1.Merge(part2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	t.Logf("Merge took: %v", time.Since(start))

	// 5. Post-Merge Accuracy (Exact Match Check)
	t.Log("=== Post-Merge Accuracy ===")

	estMerged := part1.Estimate()

	t.Logf(" Merged Estimate: %d", estMerged)
	t.Logf(" Total Estimate:  %d", estTot)

	// HLL Merge property: Registers[i] = max(A[i], B[i])
	// Since order doesn't matter for max(), the merged state MUST be bit-identical
	// to the total state.
	if estMerged != estTot {
		t.Errorf("Merge resulted in different estimate! Merged=%d, Total=%d", estMerged, estTot)
	} else {
		t.Log("PASS: Merged sketch is identical to monolithic sketch.")
	}

	// Double check internal state if possible (requires public access or deep equal)
	if !slices.Equal(part1.RegisterSlice(), totalH.RegisterSlice()) {
		t.Error("Internal Register state mismatch!")
	}
}

// =====================================================
// 5. ACCURACY REPORT (CAIDA)
// =====================================================

func TestHLL_CAIDA_AccuracyReport(t *testing.T) {
	hashes := LoadCAIDAHashes(t)

	// Ground Truth: Count unique hashes manually
	unique := make(map[uint64]bool)
	for _, h := range hashes {
		unique[h] = true
	}
	trueCardinality := len(unique)

	// Sketch Estimate
	h := hll.NewHyperLogLog()
	for _, hVal := range hashes {
		h.InsertWithHash(hVal)
	}
	estimate := h.Estimate()

	t.Log("===================================================")
	t.Logf(" HLL ACCURACY REPORT (Precision=14)")
	t.Logf(" Stream Size: %d packets", len(hashes))
	t.Log("===================================================")

	errRate := math.Abs(float64(estimate-trueCardinality)) / float64(trueCardinality)

	t.Logf(" True Cardinality: %d", trueCardinality)
	t.Logf(" Estimated Card:   %d", estimate)
	t.Logf(" Relative Error:   %.4f%%", errRate*100)
	t.Log("===================================================")

	// Theoretical Standard Error for p=14 is ~0.81%.
	// We allow 2-3 sigma (approx 2.5%) for randomness.
	if errRate > 0.025 {
		t.Errorf("Accuracy too low: %.4f%% > 2.5%%", errRate*100)
	}
}

func TestHLL_Merge_Latency_Distribution(t *testing.T) {
	hashes := LoadCAIDAHashes(t)
	mid := len(hashes) / 2

	leftSrc := hll.NewHyperLogLog()
	rightSrc := hll.NewHyperLogLog()
	for i, hash := range hashes {
		if i < mid {
			leftSrc.InsertWithHash(hash)
		} else {
			rightSrc.InsertWithHash(hash)
		}
	}

	sampleSize := 1_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left := hll.NewHyperLogLog()
		right := hll.NewHyperLogLog()
		_ = left.Merge(leftSrc)
		_ = right.Merge(rightSrc)

		start := time.Now()
		_ = left.Merge(right)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== HLL Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("======================================")
}
