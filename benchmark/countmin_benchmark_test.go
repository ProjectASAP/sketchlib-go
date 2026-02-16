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
	countminsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountMinSketch"
	"github.com/approx-telemetry/sketchlib-go/testdata"
)

const (
	cmBenchRows = 5
	cmBenchCols = 2048
)

// =====================================================
// 0. DATA LOADING HELPERS
// =====================================================

func LoadCAIDAHelper(tb testing.TB) ([]uint64, int) {
	// Adjust path relative to where test is run
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

// BenchmarkCountMin_Insert_Single measures the cost of function call overhead + insertion
func BenchmarkCountMin_Insert_Single(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cms.InsertWithHash(hashes[i%n])
	}
}

// BenchmarkCountMin_Insert_Batch simulates processing a "batch" of data.
func BenchmarkCountMin_Insert_Batch(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	batchSize := 1000

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Process a full batch per benchmark iteration
		start := (i * batchSize) % n
		end := start + batchSize
		if end > n {
			end = n
		}

		for k := start; k < end; k++ {
			cms.InsertWithHash(hashes[k])
		}
	}
}

// =====================================================
// 2. QUERY THROUGHPUT & LATENCY
// =====================================================

func BenchmarkCountMin_Query_Throughput(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	// Pre-fill
	for _, h := range hashes {
		cms.InsertWithHash(h)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

// TestCountMin_Query_Latency_P99 manually calculates latency distribution
func TestCountMin_Query_Latency_P99(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	// Pre-fill
	for _, h := range hashes {
		cms.InsertWithHash(h)
	}

	// Measure 100k queries
	sampleSize := 100_000
	latencies := make([]int64, sampleSize)

	for i := 0; i < sampleSize; i++ {
		h := hashes[i%n]
		start := time.Now()
		_, _ = cms.QueryWithHash(common.QueryFrequency, h)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[int(float64(sampleSize)*0.50)]
	p99 := latencies[int(float64(sampleSize)*0.99)]
	p999 := latencies[int(float64(sampleSize)*0.999)]

	t.Log("=== Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", p50)
	t.Logf(" P99:          %d ns", p99)
	t.Logf(" P99.9:        %d ns", p999)
	t.Log("============================")
}

// =====================================================
// 3. MEMORY USAGE
// =====================================================

func TestCountMin_Memory_Usage(t *testing.T) {
	runtime.GC() // Clear garbage
	var m1, m2 runtime.MemStats

	// Snapshot before
	runtime.ReadMemStats(&m1)

	// Allocate Sketch
	rows, cols := 5, 100_000 // Large enough to see clearly
	cms, _ := countminsketch.NewCountMinSketch(rows, cols)

	// Snapshot after allocation
	runtime.ReadMemStats(&m2)

	// Force heap retention
	runtime.KeepAlive(cms)

	// Theoretical Size (approx)
	// Matrix: rows * cols * 8 bytes (float64/int64)
	theoretical := uint64(rows * cols * 8)

	// Measured Heap Growth
	heapGrowth := m2.HeapAlloc - m1.HeapAlloc

	t.Log("=== Memory Usage Report ===")
	t.Logf(" Dimensions:      %d x %d", rows, cols)
	t.Logf(" Theoretical:     %d bytes (%.2f MB)", theoretical, float64(theoretical)/1024/1024)
	t.Logf(" Heap Growth:     %d bytes (%.2f MB)", heapGrowth, float64(heapGrowth)/1024/1024)
	t.Logf(" Overhead Factor: %.2fx", float64(heapGrowth)/float64(theoretical))

	// Populate and check dynamic growth
	t.Log("Inserting 1M items...")
	runtime.ReadMemStats(&m1)
	for i := 0; i < 1_000_000; i++ {
		cms.InsertWithHash(uint64(i))
	}
	runtime.ReadMemStats(&m2)

	dynamicGrowth := m2.HeapAlloc - m1.HeapAlloc
	t.Logf(" Dynamic Growth:  %d bytes (Expected ~0 for static CMS)", dynamicGrowth)

	// Check Memory After Merge
	cms2, _ := countminsketch.NewCountMinSketch(rows, cols)
	runtime.ReadMemStats(&m1)
	_ = cms.Merge(cms2)
	runtime.ReadMemStats(&m2)
	mergeOverhead := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf(" Merge Overhead:  %d bytes (Should be minimal/temporary)", mergeOverhead)
	t.Log("===========================")
}

// =====================================================
// 4. MERGE PERFORMANCE & SCALABILITY
// =====================================================

// Helper to create N sketches
func createSketches(n int) []*countminsketch.CountMinSketch {
	list := make([]*countminsketch.CountMinSketch, n)
	for i := 0; i < n; i++ {
		list[i], _ = countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	}
	return list
}

// Benchmark 4A: Merge Latency (2 Sketches)
func BenchmarkCountMin_Merge_2(b *testing.B) {
	s1, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	s2, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	// Populate slightly
	s1.InsertWithHash(1)
	s2.InsertWithHash(2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

// Benchmark 4B: Merge Scalability (N Sketches)
func benchmarkMergeN(b *testing.B, count int) {
	list := createSketches(count)
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

func BenchmarkCountMin_Merge_10(b *testing.B)   { benchmarkMergeN(b, 10) }
func BenchmarkCountMin_Merge_100(b *testing.B)  { benchmarkMergeN(b, 100) }
func BenchmarkCountMin_Merge_1000(b *testing.B) { benchmarkMergeN(b, 1000) }

// Test 4C: Merge Accuracy
func TestCountMin_Merge_Accuracy(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	mid := n / 2

	// 1. Create Ground Truth Sketch (Total)
	totalCMS, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	for _, h := range hashes {
		totalCMS.InsertWithHash(h)
	}

	// 2. Create Split Sketches
	part1, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	part2, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	for i, h := range hashes {
		if i < mid {
			part1.InsertWithHash(h)
		} else {
			part2.InsertWithHash(h)
		}
	}

	// 3. Pre-Merge Analysis (Output for EACH sketch)
	// We verify that Part1 and Part2 contain partial data compared to Total
	t.Log("=== Pre-Merge Individual Statistics ===")

	// Check random 100 items to get average counts
	rng := rand.New(rand.NewSource(42))
	var sumP1, sumP2, sumTot float64
	numSamples := 100

	for i := 0; i < numSamples; i++ {
		idx := rng.Intn(n)
		h := hashes[idx]

		v1, _ := part1.QueryWithHash(common.QueryFrequency, h)
		v2, _ := part2.QueryWithHash(common.QueryFrequency, h)
		vt, _ := totalCMS.QueryWithHash(common.QueryFrequency, h)

		sumP1 += v1
		sumP2 += v2
		sumTot += vt
	}

	avgP1 := sumP1 / float64(numSamples)
	avgP2 := sumP2 / float64(numSamples)
	avgTot := sumTot / float64(numSamples)

	t.Logf(" Avg Estimate Part 1 (Partial): %.2f", avgP1)
	t.Logf(" Avg Estimate Part 2 (Partial): %.2f", avgP2)
	t.Logf(" Avg Estimate Total (Ground):   %.2f", avgTot)
	t.Logf(" Theoretical Sum (P1 + P2):     %.2f", avgP1+avgP2)
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
		estTotal, _ := totalCMS.QueryWithHash(common.QueryFrequency, h)

		diff := math.Abs(estMerged - estTotal)
		totalDiff += diff
	}

	t.Logf(" Total Difference (Merged vs Total) over %d queries: %.2f", numSamples, totalDiff)

	if totalDiff > 0.001 {
		t.Error("Merge accuracy mismatch! Merged sketch differs from monolithic sketch.")
	} else {
		t.Log("PASS: Merged sketch is identical to monolithic sketch.")
	}
}
