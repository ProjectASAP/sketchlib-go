package countsketch

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// TestCS_Statistical_Quality runs a simulation to measure the actual accuracy.
// RUN WITH: go test -v -run=TestCS_Statistical_Quality
func TestCS_Statistical_Quality(t *testing.T) {
	// 1. Configuration
	// Increased columns to 4096 to handle 100k items with acceptable noise
	rows, cols := 5, 4096
	totalItems := 10_000
	topKSize := 100

	// CountSketch is noisy, so we set realistic expectations
	maxAllowedARE := 5.0 // Allow up to 5% error (Standard for probabilistic sketch)
	minPrecision := 90.0 // Expect high precision for heavy hitters

	cs, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 2. Generate Data
	truth := make(map[string]int64)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	t.Logf("generating and ingesting %d items...", totalItems)

	for i := 0; i < totalItems; i++ {
		var key string
		r := rng.Float64()
		if r < 0.8 {
			// 80% traffic goes to 20 keys (Heavy Hitters)
			key = fmt.Sprintf("heavy_%d", rng.Intn(20))
		} else {
			// 20% traffic goes to 5000 random keys (Long Tail)
			key = fmt.Sprintf("tail_%d", rng.Intn(5000))
		}

		truth[key]++
		cs.UpdateString(key, 1.0)
	}

	// 3. Prepare Ground Truth
	type kv struct {
		Key   string
		Count int64
	}
	var sortedTruth []kv
	for k, v := range truth {
		sortedTruth = append(sortedTruth, kv{k, v})
	}
	sort.Slice(sortedTruth, func(i, j int) bool {
		return sortedTruth[i].Count > sortedTruth[j].Count
	})

	// 4. Calculate Metrics
	realTopK := make(map[string]bool)
	limit := topKSize
	if len(sortedTruth) < limit {
		limit = len(sortedTruth)
	}

	for i := 0; i < limit; i++ {
		realTopK[sortedTruth[i].Key] = true
	}

	foundInHeap := 0
	if cs.TopK != nil {
		for _, item := range cs.TopK.Heap {
			if realTopK[item.Key] {
				foundInHeap++
			}
		}
	}
	precision := float64(foundInHeap) / float64(limit) * 100.0

	var totalRelErr float64
	for i := 0; i < limit; i++ {
		item := sortedTruth[i]
		est := cs.EstimateStringCount(item.Key)

		diff := math.Abs(float64(est - item.Count))
		relErr := diff / float64(item.Count)
		totalRelErr += relErr
	}
	are := (totalRelErr / float64(limit)) * 100.0

	// 5. Log Detailed Report
	t.Log("==================================================================")
	t.Logf(" COUNT SKETCH ACCURACY REPORT (N=%d, Size=%dx%d)", totalItems, rows, cols)
	t.Log("==================================================================")
	t.Logf(" True Top-%d Items identified: %d/%d", limit, foundInHeap, limit)
	t.Logf(" -> Precision Rate:        %.2f%%", precision)
	t.Logf(" -> Avg Relative Error:    %.4f%% (on top %d items)", are, limit)
	t.Log("==================================================================")

	// 6. Assertions
	if precision < minPrecision {
		t.Errorf("FAIL: Precision %.2f%% is below threshold %.2f%%", precision, minPrecision)
	}
	if are > maxAllowedARE {
		t.Errorf("FAIL: Error Rate %.2f%% is above threshold %.2f%%", are, maxAllowedARE)
	}
}
