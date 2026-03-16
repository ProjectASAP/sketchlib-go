package benchmark

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
)

const (
	csBenchRows = 5
	csBenchCols = 2048
)

// =====================================================
// 1. UPDATE THROUGHPUT
// =====================================================

// BenchmarkCountSketch_Insert_Single measures function call overhead + insertion
func BenchmarkCountSketch_Insert_Single(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cs.InsertWithHash(hashes[i%n])
	}
}

// BenchmarkCountSketch_Insert_Batch simulates processing data in batches
func BenchmarkCountSketch_Insert_Batch(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

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
			cs.InsertWithHash(hashes[k])
		}
	}
}

// BenchmarkCountSketch_Update_Speed reports sustained single-thread update speed.
func BenchmarkCountSketch_Update_Speed(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(8)

	for i := 0; i < b.N; i++ {
		cs.InsertWithHashAndValue(hashes[i%n], 1)
	}
}

// TestCountSketch_Insert_Latency_P50P99 reports insertion latency distribution.
func TestCountSketch_Insert_Latency_P50P99(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	sampleSize := minInt(100_000, n)
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		cs.InsertWithHash(hashes[i%n])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := percentileInt64(latencies, 0.50)
	p99 := percentileInt64(latencies, 0.99)

	t.Log("=== CountSketch Insert Latency Report ===")
	t.Logf(" Samples:       %d", sampleSize)
	t.Logf(" P50 (Median):  %d ns", p50)
	t.Logf(" P99:           %d ns", p99)
	t.Log("=========================================")
}

// BenchmarkCountSketch_Insert_Parallel measures sharded parallel ingestion.
func BenchmarkCountSketch_Insert_Parallel(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)

	var counter uint64
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
		for pb.Next() {
			idx := atomic.AddUint64(&counter, 1) - 1
			cs.InsertWithHash(hashes[idx%uint64(n)])
		}
		runtime.KeepAlive(cs)
	})
}

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

func BenchmarkCountSketch_Query_Throughput(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	// Pre-fill
	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

// TestCountSketch_Query_Latency_P99 calculates latency distribution
func TestCountSketch_Query_Latency_P99(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	// Pre-fill
	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	// Measure 100k queries
	sampleSize := 100_000
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		h := hashes[i%n]
		start := time.Now()
		_, _ = cs.QueryWithHash(common.QueryFrequency, h)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := percentileInt64(latencies, 0.50)
	p99 := percentileInt64(latencies, 0.99)
	p999 := percentileInt64(latencies, 0.999)

	t.Log("=== CountSketch Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("========================================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestCountSketch_Memory_Usage(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate Sketch
	// CountSketch requires cols to be power-of-two.
	// CountSketch uses float64 counters (8 bytes) + L2 Array (Rows * 8 bytes)
	rows, cols := 5, 131_072
	cs, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountSketch failed: %v", err)
	}

	// Snapshot after allocation
	runtime.ReadMemStats(&m2)

	// Force heap retention
	runtime.KeepAlive(cs)

	// Theoretical Size (approx)
	// Matrix: rows * cols * 8 bytes
	// L2: rows * 8 bytes
	theoretical := uint64((rows * cols * 8) + (rows * 8))

	// Measured Heap Growth
	heapGrowth := m2.HeapAlloc - m1.HeapAlloc

	t.Log("=== CountSketch Memory Usage Report ===")
	t.Logf(" Dimensions:      %d x %d", rows, cols)
	t.Logf(" Memory Footprint:%d bytes (%.2f MB)", heapGrowth, float64(heapGrowth)/1024/1024)
	t.Logf(" Theoretical:     %d bytes (%.2f MB)", theoretical, float64(theoretical)/1024/1024)
	t.Logf(" Heap Growth:     %d bytes (%.2f MB)", heapGrowth, float64(heapGrowth)/1024/1024)
	t.Logf(" Overhead Factor: %.2fx", float64(heapGrowth)/float64(theoretical))

	// Populate and check dynamic growth
	t.Log("Inserting 1M items...")
	runtime.ReadMemStats(&m1)
	for i := 0; i < 1_000_000; i++ {
		cs.InsertWithHash(uint64(i))
	}
	runtime.ReadMemStats(&m2)

	dynamicGrowth := m2.HeapAlloc - m1.HeapAlloc
	t.Logf(" Dynamic Growth:  %d bytes (Expected ~0 for static matrix)", dynamicGrowth)

	// Check Memory After Merge
	cs2, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountSketch for merge failed: %v", err)
	}
	runtime.ReadMemStats(&m1)
	_ = cs.Merge(cs2)
	runtime.ReadMemStats(&m2)
	mergeOverhead := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf(" Merge Overhead:  %d bytes", mergeOverhead)
	t.Log("=======================================")
}

func TestCountSketch_HeavyHitter_Recall(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	groundTruth := make(map[uint64]int)
	for _, h := range hashes {
		cs.InsertWithHash(h)
		groundTruth[h]++
	}

	type kv struct {
		Hash  uint64
		Count int
	}

	items := make([]kv, 0, len(groundTruth))
	for h, c := range groundTruth {
		items = append(items, kv{Hash: h, Count: c})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Hash < items[j].Hash
		}
		return items[i].Count > items[j].Count
	})

	topK := minInt(100, len(items))
	if topK == 0 {
		t.Fatal("empty CAIDA dataset")
	}

	actualTop := items[:topK]
	estimatedTop := make([]kv, 0, len(items))
	for _, item := range items {
		est, _ := cs.QueryWithHash(common.QueryFrequency, item.Hash)
		estimatedTop = append(estimatedTop, kv{Hash: item.Hash, Count: int(math.Round(est))})
	}

	sort.Slice(estimatedTop, func(i, j int) bool {
		if estimatedTop[i].Count == estimatedTop[j].Count {
			return estimatedTop[i].Hash < estimatedTop[j].Hash
		}
		return estimatedTop[i].Count > estimatedTop[j].Count
	})

	estimatedSet := make(map[uint64]struct{}, topK)
	for _, item := range estimatedTop[:topK] {
		estimatedSet[item.Hash] = struct{}{}
	}

	hits := 0
	for _, item := range actualTop {
		if _, ok := estimatedSet[item.Hash]; ok {
			hits++
		}
	}

	recall := float64(hits) / float64(topK)

	t.Log("=== CountSketch Heavy Hitter Recall ===")
	t.Logf(" Dataset Size:         %d", n)
	t.Logf(" Heavy Hitter Top-K:   %d", topK)
	t.Logf(" Recall:               %.4f", recall)
	t.Logf(" Hits:                 %d/%d", hits, topK)
	t.Log("======================================")
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

// Helper to create N CountSketches
func createCountSketches(n int) []*countsketch.CountSketch {
	list := make([]*countsketch.CountSketch, n)
	for i := 0; i < n; i++ {
		list[i], _ = countsketch.NewCountSketch(csBenchRows, csBenchCols)
	}
	return list
}

// Benchmark 4A: Merge Latency (2 Sketches)
func BenchmarkCountSketch_Merge_2(b *testing.B) {
	s1, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	s2, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	s1.InsertWithHash(1)
	s2.InsertWithHash(2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkCountSketchMergeN(b *testing.B, count int) {
	list := createCountSketches(count)
	for i, sk := range list {
		sk.InsertWithHash(uint64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := list[0]
		for j := 1; j < count; j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkCountSketch_Merge_10(b *testing.B)   { benchmarkCountSketchMergeN(b, 10) }
func BenchmarkCountSketch_Merge_100(b *testing.B)  { benchmarkCountSketchMergeN(b, 100) }
func BenchmarkCountSketch_Merge_1000(b *testing.B) { benchmarkCountSketchMergeN(b, 1000) }

// Test 4C: Merge Accuracy
func TestCountSketch_Merge_Accuracy(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	mid := n / 2

	// 1. Create Ground Truth Sketch (Total)
	totalCS, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	for _, h := range hashes {
		totalCS.InsertWithHash(h)
	}

	// 2. Create Split Sketches
	part1, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	part2, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	for i, h := range hashes {
		if i < mid {
			part1.InsertWithHash(h)
		} else {
			part2.InsertWithHash(h)
		}
	}

	// 3. Pre-Merge Analysis
	t.Log("=== Pre-Merge Individual Statistics ===")
	rng := rand.New(rand.NewSource(42))
	var sumP1, sumP2, sumTot float64
	numSamples := 100

	for i := 0; i < numSamples; i++ {
		idx := rng.Intn(n)
		h := hashes[idx]

		v1, _ := part1.QueryWithHash(common.QueryFrequency, h)
		v2, _ := part2.QueryWithHash(common.QueryFrequency, h)
		vt, _ := totalCS.QueryWithHash(common.QueryFrequency, h)

		sumP1 += v1
		sumP2 += v2
		sumTot += vt
	}

	avgP1 := sumP1 / float64(numSamples)
	avgP2 := sumP2 / float64(numSamples)
	avgTot := sumTot / float64(numSamples)

	t.Logf(" Avg Estimate Part 1: %.2f", avgP1)
	t.Logf(" Avg Estimate Part 2: %.2f", avgP2)
	t.Logf(" Avg Estimate Total:  %.2f", avgTot)
	t.Logf(" Theoretical Sum:     %.2f", avgP1+avgP2)
	t.Log("=======================================")

	// 4. Merge
	t.Log("Merging Part 2 into Part 1...")
	start := time.Now()
	err := part1.Merge(part2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	t.Logf("Merge took: %v", time.Since(start))

	// 5. Post-Merge Accuracy
	t.Log("=== Post-Merge Accuracy ===")

	var totalDiff float64
	for i := 0; i < numSamples; i++ {
		idx := rng.Intn(n)
		h := hashes[idx]

		estMerged, _ := part1.QueryWithHash(common.QueryFrequency, h)
		estTotal, _ := totalCS.QueryWithHash(common.QueryFrequency, h)

		diff := math.Abs(estMerged - estTotal)
		totalDiff += diff
	}

	t.Logf(" Total Difference (Merged vs Total) over %d queries: %.2f", numSamples, totalDiff)

	// CountSketch uses float64, allowing for tiny precision drift, but it should be near zero
	if totalDiff > 0.001 {
		t.Error("Merge accuracy mismatch! Merged sketch differs from monolithic sketch.")
	} else {
		t.Log("PASS: Merged sketch is identical to monolithic sketch.")
	}
}

func TestCountSketch_Merge_Latency_Distribution(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	mid := n / 2

	leftSrc, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	rightSrc, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	for i, h := range hashes {
		if i < mid {
			leftSrc.InsertWithHash(h)
		} else {
			rightSrc.InsertWithHash(h)
		}
	}

	sampleSize := 1_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
		right, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
		_ = left.Merge(leftSrc)
		_ = right.Merge(rightSrc)

		start := time.Now()
		_ = left.Merge(right)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== CountSketch Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==============================================")
}

func percentileInt64(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(values)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
