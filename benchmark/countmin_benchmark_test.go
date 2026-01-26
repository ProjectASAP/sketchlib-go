package benchmark

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	countminsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountMinSketch"
)

// =====================================================
// 1. ACCURACY REPORT
// =====================================================

func TestCountMinSketch_AccuracyReport(t *testing.T) {
	rows, cols := 5, 1024
	totalItems := 100_000
	topKSize := 100
	vocabSize := 10_000

	cms, err := countminsketch.NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	groundTruth := make(map[string]int64)
	rng := rand.New(rand.NewSource(42)) // deterministic

	for i := 0; i < totalItems; i++ {
		var key string
		if rng.Float64() < 0.8 {
			key = fmt.Sprintf("hot_%d", rng.Intn(vocabSize/50))
		} else {
			key = fmt.Sprintf("cold_%d", rng.Intn(vocabSize))
		}

		groundTruth[key]++
		cms.InsertWithHash(common.FromString(key).Hash)
	}

	// ---- sort truth ----
	type kv struct {
		Key   string
		Count int64
	}
	var truth []kv
	for k, v := range groundTruth {
		truth = append(truth, kv{k, v})
	}
	sort.Slice(truth, func(i, j int) bool {
		return truth[i].Count > truth[j].Count
	})

	limit := topKSize
	if len(truth) < limit {
		limit = len(truth)
	}

	// ---- Measure overestimation ----
	var totalOver float64
	for i := 0; i < limit; i++ {
		key := truth[i].Key
		trueCnt := truth[i].Count
		est, _ := cms.QueryWithHash(common.QueryFrequency, common.FromString(key).Hash)

		if est < float64(trueCnt) {
			t.Fatalf("CMS underestimation detected (impossible)")
		}
		totalOver += (est - float64(trueCnt)) / float64(trueCnt)
	}

	avgOver := totalOver / float64(limit) * 100

	t.Log("===================================================")
	t.Logf(" COUNT-MIN SKETCH ACCURACY REPORT (N=%d, %dx%d)", totalItems, rows, cols)
	t.Log("===================================================")
	t.Logf(" Avg Relative Overestimation (Top-%d): %.4f%%", limit, avgOver)
	t.Log("===================================================")

	if avgOver > 10 {
		t.Errorf("overestimation too high")
	}
}

//
// ==========================
// 2. MICRO BENCHMARKS
// ==========================
//

const (
	cmBenchRows = 5
	cmBenchCols = 1024
	cmBenchN    = 1 << 20
)

func makeCMHashedInputs(n int) []uint64 {
	hashes := make([]uint64, n)
	for i := 0; i < n; i++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(i))
		hashes[i] = common.Hash64(b[:])
	}
	return hashes
}

func makeCMStringInputs(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("item_%d", i)
	}
	return out
}

// ------------------------------------------
// Benchmark 1: InsertWithHash (fast path)
// ------------------------------------------
func BenchmarkCountMinSketch_InsertWithHash(b *testing.B) {
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	hashes := makeCMHashedInputs(cmBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cms.InsertWithHash(hashes[i&(cmBenchN-1)])
	}
}

// ------------------------------------------
// Benchmark 2: End-to-end insert (hash + insert)
// ------------------------------------------
func BenchmarkCountMinSketch_InsertEndToEnd(b *testing.B) {
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	keys := makeCMStringInputs(cmBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := common.FromString(keys[i&(cmBenchN-1)]).Hash
		cms.InsertWithHash(h)
	}
}

// ------------------------------------------
// Benchmark 3: QueryFrequency
// ------------------------------------------
func BenchmarkCountMinSketch_QueryFrequency(b *testing.B) {
	cms, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	hashes := makeCMHashedInputs(cmBenchN)

	for i := 0; i < 10_000; i++ {
		cms.InsertWithHash(hashes[i])
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[i&(cmBenchN-1)])
	}
}

// ------------------------------------------
// Benchmark 4: Merge (distributed scenario)
// ------------------------------------------
func BenchmarkCountMinSketch_Merge(b *testing.B) {
	cms1, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)
	cms2, _ := countminsketch.NewCountMinSketch(cmBenchRows, cmBenchCols)

	hashes := makeCMHashedInputs(100_000)
	for _, h := range hashes {
		cms1.InsertWithHash(h)
		cms2.InsertWithHash(h)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cms1.Merge(cms2)
	}
}
