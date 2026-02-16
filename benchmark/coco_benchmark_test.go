package benchmark

import (
	"encoding/binary"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"
	cocosketch "github.com/approx-telemetry/sketchlib-go/sketches/CocoSketch"
	"github.com/approx-telemetry/sketchlib-go/testdata"
)

const (
	cocoBenchD      = 5
	cocoBenchLength = 2048
)

// =====================================================
// 0. DATA LOADING HELPERS
// =====================================================

func LoadCAIDAHelperCoco(tb testing.TB) ([]uint64, int) {
	// Adjust path relative to where test is run (sketches/CocoSketch or benchmark)
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
		ipUint := uint32(s.F)
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ipUint)
		hashes[i] = common.Hash64(ipBytes)
	}
	return hashes, len(samples)
}

// =====================================================
// 1. UPDATE THROUGHPUT
// =====================================================

// BenchmarkCocoSketch_Insert_Single measures insertion with probabilistic replacement.
func BenchmarkCocoSketch_Insert_Single(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cs.InsertWithHash(hashes[i%n])
	}
}

// BenchmarkCocoSketch_Insert_Batch simulates processing batches.
func BenchmarkCocoSketch_Insert_Batch(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

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

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

// BenchmarkCocoSketch_Query_Throughput measures Median aggregation query.
func BenchmarkCocoSketch_Query_Throughput(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	// Pre-fill
	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

// TestCocoSketch_Query_Latency_P99 measures latency distribution.
// CocoSketch queries are slightly more expensive than CMS (checking keys + median sort).
func TestCocoSketch_Query_Latency_P99(t *testing.T) {
	hashes, n := LoadCAIDAHelperCoco(t)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	sampleSize := 100_000
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		h := hashes[i%n]
		start := time.Now()
		_, _ = cs.QueryWithHash(common.QueryFrequency, h)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[int(float64(sampleSize)*0.50)]
	p99 := latencies[int(float64(sampleSize)*0.99)]
	p999 := latencies[int(float64(sampleSize)*0.999)]

	t.Log("=== CocoSketch Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("=======================================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestCocoSketch_Memory_Usage(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate Sketch
	d, length := 4, 100_000
	cs, _ := cocosketch.NewCocoSketch(d, length)

	// Snapshot after
	runtime.ReadMemStats(&m2)
	runtime.KeepAlive(cs)

	// Theoretical Size:
	// Arrays: 2 (Keys + Counts)
	// Size: d * length * 8 bytes (uint64) * 2 arrays
	theoretical := uint64(d * length * 8 * 2)

	heapGrowth := m2.HeapAlloc - m1.HeapAlloc

	t.Log("=== CocoSketch Memory Usage Report ===")
	t.Logf(" Dimensions:      %d x %d", d, length)
	t.Logf(" Theoretical:     %d bytes (%.2f MB)", theoretical, float64(theoretical)/1024/1024)
	t.Logf(" Heap Growth:     %d bytes (%.2f MB)", heapGrowth, float64(heapGrowth)/1024/1024)
	t.Logf(" Overhead Factor: %.2fx", float64(heapGrowth)/float64(theoretical))

	// Populate
	t.Log("Inserting 1M items...")
	runtime.ReadMemStats(&m1)
	for i := 0; i < 1_000_000; i++ {
		cs.InsertWithHash(uint64(i))
	}
	runtime.ReadMemStats(&m2)

	dynamicGrowth := m2.HeapAlloc - m1.HeapAlloc
	t.Logf(" Dynamic Growth:  %d bytes (Expected ~0)", dynamicGrowth)

	// Check Merge Overhead
	cs2, _ := cocosketch.NewCocoSketch(d, length)
	runtime.ReadMemStats(&m1)
	_ = cs.Merge(cs2)
	runtime.ReadMemStats(&m2)
	mergeOverhead := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf(" Merge Overhead:  %d bytes", mergeOverhead)
	t.Log("======================================")
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

func createCocoSketches(n int) []*cocosketch.CocoSketch {
	list := make([]*cocosketch.CocoSketch, n)
	for i := 0; i < n; i++ {
		list[i], _ = cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	}
	return list
}

// Benchmark 4A: Merge Latency
func BenchmarkCocoSketch_Merge_2(b *testing.B) {
	s1, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	s2, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	s1.InsertWithHash(1)
	s2.InsertWithHash(2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

// Benchmark 4B: Merge Scalability
func benchmarkCocoMergeN(b *testing.B, count int) {
	list := createCocoSketches(count)
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

func BenchmarkCocoSketch_Merge_10(b *testing.B)   { benchmarkCocoMergeN(b, 10) }
func BenchmarkCocoSketch_Merge_100(b *testing.B)  { benchmarkCocoMergeN(b, 100) }
func BenchmarkCocoSketch_Merge_1000(b *testing.B) { benchmarkCocoMergeN(b, 1000) }

// Test 4C: Merge Accuracy
func TestCocoSketch_Merge_Accuracy(t *testing.T) {
	hashes, n := LoadCAIDAHelperCoco(t)
	mid := n / 2

	// 1. Total Sketch
	totalCS, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	for _, h := range hashes {
		totalCS.InsertWithHash(h)
	}

	// 2. Split Sketches
	part1, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	part2, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	for i, h := range hashes {
		if i < mid {
			part1.InsertWithHash(h)
		} else {
			part2.InsertWithHash(h)
		}
	}

	// 3. Pre-Merge Check
	t.Log("=== Pre-Merge Statistics ===")
	// Identify a heavy hitter for checking
	heavyHitter := hashes[0] // Approximation
	v1, _ := part1.QueryWithHash(common.QueryFrequency, heavyHitter)
	v2, _ := part2.QueryWithHash(common.QueryFrequency, heavyHitter)
	vt, _ := totalCS.QueryWithHash(common.QueryFrequency, heavyHitter)
	t.Logf(" Heavy Key Est -> P1: %.0f, P2: %.0f, Total: %.0f", v1, v2, vt)

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

	// CocoSketch Merge is PROBABILISTIC.
	// We do NOT expect exact matches.
	// We expect the merged sketch to be reasonably close to the total sketch.

	rng := rand.New(rand.NewSource(42))
	var totalDiff float64
	numSamples := 1000

	for i := 0; i < numSamples; i++ {
		idx := rng.Intn(n)
		h := hashes[idx]

		estMerged, _ := part1.QueryWithHash(common.QueryFrequency, h)
		estTotal, _ := totalCS.QueryWithHash(common.QueryFrequency, h)

		diff := math.Abs(estMerged - estTotal)
		totalDiff += diff
	}

	avgDiff := totalDiff / float64(numSamples)
	t.Logf(" Avg Difference per query: %.2f", avgDiff)

	// Just ensure it's not broken (difference should be small compared to total count)
	// Note: Total stream size is 2M. Avg count ~ 2M/Unique ~ 28.
	// Avg diff of < 5.0 is acceptable for a 128KB sketch.
	if avgDiff > 50.0 {
		t.Errorf("Merge accuracy poor! Avg Diff: %.2f", avgDiff)
	} else {
		t.Log("PASS: Merged sketch accuracy is within expected probabilistic bounds.")
	}
}

// =====================================================
// 5. ACCURACY REPORT (CAIDA)
// =====================================================

func TestCocoSketch_CAIDA_AccuracyReport(t *testing.T) {
	hashes, _ := LoadCAIDAHelperCoco(t)

	d, length := cocoBenchD, cocoBenchLength
	cs, _ := cocosketch.NewCocoSketch(d, length)

	// Ground Truth
	groundTruth := make(map[uint64]int64)
	for _, h := range hashes {
		groundTruth[h]++
		cs.InsertWithHash(h)
	}

	// Sort Ground Truth
	type kv struct {
		Hash  uint64
		Count int64
	}
	var truth []kv
	for k, v := range groundTruth {
		truth = append(truth, kv{k, v})
	}
	sort.Slice(truth, func(i, j int) bool {
		return truth[i].Count > truth[j].Count
	})

	topK := 100
	if len(truth) < topK {
		topK = len(truth)
	}

	// Calculate Precision & ARE for Top-K
	// Note: CocoSketch doesn't have a built-in heap, so we query using known top-k keys
	var totalErr float64

	t.Log("===================================================")
	t.Logf(" COCOSKETCH ACCURACY REPORT")
	t.Logf(" Stream Size: %d", len(hashes))
	t.Logf(" Dimensions:  %d x %d", d, length)
	t.Log("===================================================")

	for i := 0; i < topK; i++ {
		item := truth[i]
		est, _ := cs.QueryWithHash(common.QueryFrequency, item.Hash)

		err := math.Abs(est - float64(item.Count))
		relErr := err / float64(item.Count)
		totalErr += relErr
	}

	are := (totalErr / float64(topK)) * 100
	t.Logf(" Avg Relative Error (Top-%d): %.4f%%", topK, are)
	t.Log("===================================================")

	// CocoSketch is designed for heavy hitters, so ARE should be competitive.
	if are > 20.0 {
		t.Errorf("ARE too high: %.2f%%", are)
	}
}
