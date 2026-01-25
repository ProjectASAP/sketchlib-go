package benchmark

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	countsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountSketch"
)

//
// =====================================================
// 1. ACCURACY / CORRECTNESS BENCH TEST
// =====================================================
//
// NOTE: This is NOT a microbenchmark.
// This test prints accuracy metrics for evaluation.
//

func TestCountSketch_AccuracyReport(t *testing.T) {
	rows, cols := 5, 2048
	topKSize := 100
	totalItems := 100_000
	vocabSize := 10_000

	cs, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	groundTruth := make(map[string]int64)
	rng := rand.New(rand.NewSource(42)) // deterministic

	t.Logf("Generating %d items (skewed distribution)...", totalItems)

	for i := 0; i < totalItems; i++ {
		var key string
		if rng.Float64() < 0.8 {
			key = fmt.Sprintf("hot_%d", rng.Intn(vocabSize/50))
		} else {
			key = fmt.Sprintf("cold_%d", rng.Intn(vocabSize))
		}
		groundTruth[key]++
		cs.UpdateString(key, 1)
	}

	// ---- Ground truth sort ----
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

	trueTopK := make(map[string]bool)
	for i := 0; i < limit; i++ {
		trueTopK[truth[i].Key] = true
	}

	// ---- Precision ----
	found := 0
	for _, item := range cs.TopK.Heap {
		if trueTopK[item.Key] {
			found++
		}
	}
	precision := float64(found) / float64(limit) * 100

	// ---- ARE ----
	var totalErr float64
	for i := 0; i < limit; i++ {
		est := cs.EstimateStringCount(truth[i].Key)
		err := math.Abs(float64(est-truth[i].Count)) / float64(truth[i].Count)
		totalErr += err
	}
	are := totalErr / float64(limit) * 100

	// ---- REPORT ----
	t.Log("===================================================")
	t.Logf(" COUNTSKETCH ACCURACY REPORT (N=%d, %dx%d)", totalItems, rows, cols)
	t.Log("===================================================")
	t.Logf(" Top-%d Precision: %.2f%% (%d/%d)", limit, precision, found, limit)
	t.Logf(" Avg Relative Error (Top-%d): %.4f%%", limit, are)
	t.Log("===================================================")

	if precision < 80 {
		t.Errorf("precision too low")
	}
	if are > 5 {
		t.Errorf("ARE too high")
	}
}

//
// ==========================
// 2. MICRO BENCHMARKS
// ==========================
//

const (
	csBenchRows = 5
	csBenchCols = 2048
	csBenchN    = 1 << 20 // power of two for masking
)

func makeHashedInputs(n int) []uint64 {
	hashes := make([]uint64, n)
	for i := 0; i < n; i++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(i))
		hashes[i] = common.Hash64(b[:])
	}
	return hashes
}

func makeStringInputs(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("item_%d", i)
	}
	return out
}

// ------------------------------------------
// Benchmark 1: InsertWithHash (fast path)
// ------------------------------------------
func BenchmarkCountSketch_InsertWithHash(b *testing.B) {
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	hashes := makeHashedInputs(csBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cs.InsertWithHash(hashes[i&(csBenchN-1)])
	}
}

// ------------------------------------------
// Benchmark 2: UpdateString (hash + TopK)
// ------------------------------------------
func BenchmarkCountSketch_UpdateString(b *testing.B) {
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	keys := makeStringInputs(csBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cs.UpdateString(keys[i&(csBenchN-1)], 1)
	}
}

// ------------------------------------------
// Benchmark 3: QueryFrequency
// ------------------------------------------
func BenchmarkCountSketch_QueryFrequency(b *testing.B) {
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	hashes := makeHashedInputs(csBenchN)

	for i := 0; i < 10_000; i++ {
		cs.InsertWithHash(hashes[i])
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i&(csBenchN-1)])
	}
}

// ------------------------------------------
// Benchmark 4: Merge Cost
// ------------------------------------------
func BenchmarkCountSketch_Merge(b *testing.B) {
	cs1, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	cs2, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)

	hashes := makeHashedInputs(100_000)
	for _, h := range hashes {
		cs1.InsertWithHash(h)
		cs2.InsertWithHash(h)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cs1.Merge(cs2)
	}
}
