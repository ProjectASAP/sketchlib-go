package benchmark

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"
	// Pastikan path ini sesuai dengan struktur folder Anda
	// Gunakan alias 'countsketch' untuk membedakan dari nama struct
	countsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountSketch"
)

// ==========================================
// 1. CORRECTNESS / ACCURACY TEST
// ==========================================

// TestCountSketch_Accuracy measures the precision of the sketch using skewed data.
func TestCountSketch_Accuracy(t *testing.T) {
	// Setup khusus untuk test ini (lokal scope, aman dari konflik)
	rows, cols := 5, 2048
	topKSize := 100
	totalItems := 100_000
	vocabSize := 10_000

	// Init Sketch menggunakan package prefix 'countsketch'
	cs, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("Failed to init sketch: %v", err)
	}

	// Create Ground Truth (Exact Map)
	groundTruth := make(map[string]int64)

	// Generate Skewed Data (Zipfian-like simple simulation)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	fmt.Println(">>> Generating Data & Ingesting...")
	for i := 0; i < totalItems; i++ {
		var key string
		r := rng.Float64()
		if r < 0.8 {
			// Hot items (80% traffic to 2% items)
			id := rng.Intn(vocabSize / 50)
			key = fmt.Sprintf("item_%d", id)
		} else {
			// Cold items
			id := rng.Intn(vocabSize)
			key = fmt.Sprintf("item_%d", id)
		}

		groundTruth[key]++
		cs.UpdateString(key, 1.0)
	}

	// --- ANALYSIS ---

	// 1. Sort Ground Truth
	type kv struct {
		Key   string
		Count int64
	}
	var truthList []kv
	for k, v := range groundTruth {
		truthList = append(truthList, kv{k, v})
	}
	sort.Slice(truthList, func(i, j int) bool {
		return truthList[i].Count > truthList[j].Count
	})

	// Get True Top-K keys
	trueTopKSet := make(map[string]bool)
	limit := topKSize
	if len(truthList) < limit {
		limit = len(truthList)
	}
	for i := 0; i < limit; i++ {
		trueTopKSet[truthList[i].Key] = true
	}

	// 2. Measure Top-K Precision (Recall)
	sketchTopKCount := 0
	if cs.TopK != nil {
		// Akses field publik Heap
		for _, item := range cs.TopK.Heap {
			if trueTopKSet[item.Key] {
				sketchTopKCount++
			}
		}
	}
	precision := float64(sketchTopKCount) / float64(limit) * 100

	// 3. Measure Average Relative Error (ARE)
	var totalError float64
	for i := 0; i < limit; i++ {
		key := truthList[i].Key
		trueCount := truthList[i].Count

		est := cs.EstimateStringCount(key)

		diff := float64(est - trueCount)
		err := math.Abs(diff) / float64(trueCount)
		totalError += err
	}
	are := (totalError / float64(limit)) * 100

	// --- REPORT ---
	fmt.Println("========================================")
	fmt.Printf("   COUNTSKETCH ACCURACY REPORT (N=%d, Cols=%d)\n", totalItems, cols)
	fmt.Println("========================================")
	fmt.Printf("True Top-%d Items found in Sketch: %d/%d\n", limit, sketchTopKCount, limit)
	fmt.Printf(">> Top-K Precision: %.2f%%\n", precision)
	fmt.Printf(">> Avg Relative Error (Top Items): %.4f%%\n", are)
	fmt.Println("========================================")

	if precision < 80.0 {
		t.Errorf("Precision too low: got %.2f%%, want > 80%%", precision)
	}
	if are > 5.0 {
		t.Errorf("Error rate too high: got %.2f%%, want < 5%%", are)
	}
}

// ==========================================
// 2. THROUGHPUT BENCHMARKS
// ==========================================

// GANTI NAMA KONSTANTA AGAR TIDAK BENTROK
const (
	csBenchRows = 5
	csBenchCols = 2048
	csBenchN    = 1_000_000
)

// GANTI NAMA HELPER AGAR TIDAK BENTROK
func makeCSPrehashedInputs(n int) []uint64 {
	hashes := make([]uint64, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(i))
		hashes[i] = common.Hash64(b)
	}
	return hashes
}

func makeCSStringInputs(n int) []string {
	strs := make([]string, n)
	for i := 0; i < n; i++ {
		strs[i] = fmt.Sprintf("bench_item_%d", i)
	}
	return strs
}

// Benchmark 1: The "Zero-Allocation" Path
func BenchmarkCountSketch_InsertWithHash(b *testing.B) {
	// Gunakan prefix konstanta baru
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	hashes := makeCSPrehashedInputs(csBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := hashes[i&(csBenchN-1)]
		cs.InsertWithHash(h)
	}
}

// Benchmark 2: The "Full" Path (UpdateString with TopK)
func BenchmarkCountSketch_UpdateString(b *testing.B) {
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	inputs := makeCSStringInputs(csBenchN)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := inputs[i&(csBenchN-1)]
		cs.UpdateString(key, 1.0)
	}
}

// Benchmark 3: Query Performance
func BenchmarkCountSketch_Query(b *testing.B) {
	cs, _ := countsketch.NewCountSketch(csBenchRows, csBenchCols)
	hashes := makeCSPrehashedInputs(csBenchN)

	// Warmup
	for i := 0; i < 1000; i++ {
		cs.InsertWithHash(hashes[i])
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := hashes[i&(csBenchN-1)]
		_, _ = cs.QueryWithHash(common.QueryFrequency, h)
	}
}
