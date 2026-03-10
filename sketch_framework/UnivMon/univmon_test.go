package univmon

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =====================================================
// HELPER: Load CAIDA Data
// =====================================================

func LoadCAIDA(t *testing.T) ([]string, int) {
	// Adjust path relative to where test is run (sketch_framework/UnivMon)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("No samples loaded from CAIDA files")
	}

	// Convert IPs to string keys for UnivMon
	keys := make([]string, len(samples))
	for i, s := range samples {
		// Convert float IP to uint32 to string (or just use raw bytes if preferred)
		// For consistency with other tests, we'll format the uint32 IP as a string key
		ipUint := uint32(s.F)
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ipUint)

		// Use hex string or simple string representation to act as the "Key"
		keys[i] = fmt.Sprintf("%x", ipBytes)
	}

	return keys, len(samples)
}

// =====================================================
// STANDARD TESTS
// =====================================================

// TestUnivSketch_Basic verifies basic operations and statistical queries (L1, Card, Entropy)
func TestUnivSketch_Basic(t *testing.T) {
	// Configuration
	k := 100
	row := 5
	col := 1024
	layer := 8

	// 1. Initialization
	us, err := NewUnivSketchPyramid(k, row, col, layer)
	require.NoError(t, err)

	// 2. Prepare Simple Data
	cases := []struct {
		key string
		cnt int64
	}{
		{"apple", 1},
		{"banana", 1},
		{"apple", 1}, // apple total 2
		{"orange", 10},
		{"mango", 5},
	}

	// 3. Insert Loop
	totalExpected := int64(0)
	distinctExpected := 4 // apple, banana, orange, mango

	for _, c := range cases {
		input := common.FromString(c.key)
		us.Update(input, c.cnt)
		totalExpected += c.cnt
	}

	// 4. Verify L1 (Total Count)
	assert.Equal(t, totalExpected, us.bucket_size, "Bucket size (Exact L1) mismatch")

	// 5. Verify Cardinality
	cardEst := us.GetCardinality()
	// Error tolerance loose for tiny data
	assert.InDelta(t, float64(distinctExpected), cardEst, 1.5, "Cardinality estimation inaccurate")

	// 6. Verify Entropy
	// Total=18. P(apple)=2/18, P(banana)=1/18, P(orange)=10/18, P(mango)=5/18
	probs := []float64{2.0 / 18.0, 1.0 / 18.0, 10.0 / 18.0, 5.0 / 18.0}
	expectedEntropy := 0.0
	for _, p := range probs {
		expectedEntropy -= p * math.Log2(p)
	}

	entropyEst := us.GetEntropy()
	assert.InDelta(t, expectedEntropy, entropyEst, 0.5, "Entropy estimation inaccurate")
}

// TestUnivSketch_Merge verifies sketch merging logic
func TestUnivSketch_Merge(t *testing.T) {
	k, row, col, layer := 100, 5, 2048, 8

	us1, _ := NewUnivSketchPyramid(k, row, col, layer)
	us2, _ := NewUnivSketchPyramid(k, row, col, layer)

	// US1: key A = 10
	us1.Update(common.FromString("A"), 10)

	// US2: key A = 20, key B = 5
	us2.Update(common.FromString("A"), 20)
	us2.Update(common.FromString("B"), 5)

	// Merge US2 into US1
	us1.Merge(us2)

	// Expectation in US1:
	// A = 10 + 20 = 30
	// B = 0 + 5 = 5
	// Total bucket size = 35

	assert.Equal(t, int64(35), us1.bucket_size, "Bucket size incorrect after merge")

	// Check TopK to verify merged counters
	topk := us1.QueryTopK(5)

	idxA, foundA := topk.Find("A")
	require.True(t, foundA, "Key A must exist after merge")
	assert.InDelta(t, 30.0, float64(topk.Heap[idxA].Count), 2.0, "Count A after merge is incorrect")

	idxB, foundB := topk.Find("B")
	require.True(t, foundB, "Key B must exist after merge")
	assert.InDelta(t, 5.0, float64(topk.Heap[idxB].Count), 1.0, "Count B after merge is incorrect")
}

// =====================================================
// CAIDA REAL-WORLD ACCURACY TEST
// =====================================================

func TestUnivSketch_CAIDA_Accuracy(t *testing.T) {
	// 1. Load Data
	keys, n := LoadCAIDA(t)

	// 2. Configure Sketch
	// UnivMon needs decent resources for real traffic
	// k=200 heavy hitters, 5 rows, 4096 cols, 16 layers
	us, _ := NewUnivSketchPyramid(200, 5, 4096, 16)

	// 3. Ground Truth Map
	gtCounts := make(map[string]int64)

	// 4. Process Stream
	t.Logf("Processing %d CAIDA packets...", n)
	start := time.Now()

	for _, key := range keys {
		// Update Ground Truth
		gtCounts[key]++

		// Update Sketch
		us.Update(common.FromString(key), 1)
	}

	duration := time.Since(start)
	t.Logf("Processed in %v (%.2f ns/op)", duration, float64(duration.Nanoseconds())/float64(n))

	// 5. Evaluate Heavy Hitters (Top-100)
	t.Log("Querying Top-K Heavy Hitters...")
	topk := us.QueryTopK(100)

	// Sort Ground Truth to find true Top-K
	type kv struct {
		Key   string
		Count int64
	}
	var sortedGT []kv
	for k, v := range gtCounts {
		sortedGT = append(sortedGT, kv{k, v})
	}
	sort.Slice(sortedGT, func(i, j int) bool {
		return sortedGT[i].Count > sortedGT[j].Count
	})

	// 6. Accuracy Check
	// Check the top 10 heavy hitters specifically
	checkCount := 10
	if len(sortedGT) < checkCount {
		checkCount = len(sortedGT)
	}

	var totalErrRate float64

	t.Logf("%-20s | %-10s | %-10s | %-10s", "IP Key", "Real", "Est", "Error %")
	t.Log("---------------------------------------------------------------")

	for i := 0; i < checkCount; i++ {
		trueItem := sortedGT[i]

		// Find in Sketch Heap
		idx, found := topk.Find(trueItem.Key)

		if !found {
			// If it's a very heavy hitter, it SHOULD be found.
			// UnivMon guarantees finding items with freq > epsilon * N
			t.Errorf("Heavy Hitter %s (Count: %d) MISSING from TopK heap", trueItem.Key, trueItem.Count)
			continue
		}

		estVal := topk.Heap[idx].Count
		errRate := math.Abs(float64(estVal-trueItem.Count)) / float64(trueItem.Count)
		totalErrRate += errRate

		t.Logf("%-20s | %-10d | %-10d | %.2f%%", trueItem.Key, trueItem.Count, estVal, errRate*100)
	}

	avgErr := totalErrRate / float64(checkCount)
	t.Logf("Average Error for Top-%d: %.2f%%", checkCount, avgErr*100)

	// Assert reasonable accuracy (e.g., < 5% avg error for top items)
	if avgErr > 0.05 {
		t.Errorf("Accuracy too low! Avg Error %.2f%% > 5%%", avgErr*100)
	}
}
