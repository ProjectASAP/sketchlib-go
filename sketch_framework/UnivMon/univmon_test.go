package univmon

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnivSketch_Basic verifies basic operations and statistical queries (L1, Card, Entropy)
func TestUnivSketch_Basic(t *testing.T) {
	// Configuration
	k := TOPK_SIZE
	row := CS_ROW_NO_Univ_ELEPHANT
	col := CS_COL_NO_Univ_ELEPHANT
	layer := CS_LVLS

	// 1. Initialization (No manual Seed, handled internally/common)
	us, err := NewUnivSketchPyramid(k, row, col, layer)
	require.NoError(t, err)
	defer us.Free()

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

	// 3. Insert Loop (Using New API: Update)
	totalExpected := int64(0)
	distinctExpected := 4 // apple, banana, orange, mango

	for _, c := range cases {
		// Create SketchInput from string
		input := common.FromString(c.key)
		// Update sketch
		us.Update(input, c.cnt)
		totalExpected += c.cnt
	}

	// 4. Verify L1 (Total Count)
	// Since GetL1() is missing, we check bucket_size directly (Exact Count)
	assert.Equal(t, totalExpected, us.bucket_size, "Bucket size (Exact L1) mismatch")

	// 5. Verify Cardinality
	cardEst := us.GetCardinality()
	fmt.Printf("Actual Distinct: %d, Estimated: %.4f\n", distinctExpected, cardEst)
	// Error tolerance slightly loose for very small data
	assert.InDelta(t, float64(distinctExpected), cardEst, 1.5, "Cardinality estimation inaccurate")

	// 6. Verify Entropy
	// Calculate entropy manually:
	// Total=18. P(apple)=2/18, P(banana)=1/18, P(orange)=10/18, P(mango)=5/18
	// Entropy = -Sum(p * log2(p))
	probs := []float64{2.0 / 18.0, 1.0 / 18.0, 10.0 / 18.0, 5.0 / 18.0}
	expectedEntropy := 0.0
	for _, p := range probs {
		expectedEntropy -= p * math.Log2(p)
	}

	entropyEst := us.GetEntropy()
	fmt.Printf("Actual Entropy: %.4f, Estimated: %.4f\n", expectedEntropy, entropyEst)
	assert.InDelta(t, expectedEntropy, entropyEst, 0.5, "Entropy estimation inaccurate")
}

// TestUnivSketch_TopK verifies Heavy Hitters accuracy
func TestUnivSketch_TopK(t *testing.T) {
	us, _ := NewUnivSketchPyramid(TOPK_SIZE, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT, CS_LVLS)

	// Scenario: "elephant" appears 1000 times, "mouse" appears 10 times
	targetKey := "elephant"
	targetCount := int64(1000)

	inputHeavy := common.FromString(targetKey)
	us.Update(inputHeavy, targetCount)

	inputLight := common.FromString("mouse")
	us.Update(inputLight, 10)

	// Query TopK
	topk := us.QueryTopK(5) // Get top 5

	// Check if elephant exists
	idx, found := topk.Find(targetKey)
	require.True(t, found, "Heavy hitter 'elephant' must be found in TopK")

	// Check count accuracy
	estCount := topk.Heap[idx].Count
	fmt.Printf("Heavy Hitter '%s': Real=%d, Est=%d\n", targetKey, targetCount, estCount)

	// Error tolerance 5%
	assert.InDelta(t, float64(targetCount), float64(estCount), float64(targetCount)*0.05, "TopK count estimation inaccurate")
}

// TestUnivSketch_Merge verifies sketch merging
func TestUnivSketch_Merge(t *testing.T) {
	k, row, col, layer := TOPK_SIZE, 5, 2048, 8

	us1, _ := NewUnivSketchPyramid(k, row, col, layer)
	us2, _ := NewUnivSketchPyramid(k, row, col, layer)

	// US1: key A = 10
	us1.Update(common.FromString("A"), 10)

	// US2: key A = 20, key B = 5
	us2.Update(common.FromString("A"), 20)
	us2.Update(common.FromString("B"), 5)

	// Merge US2 ke US1
	us1.MergeWith(us2)

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

// TestAccuracy_Syntethic similar to your original test using Zipf distribution
func TestAccuracy_Syntethic(t *testing.T) {

	us, _ := NewUnivSketchPyramid(100, 5, 4096, 16)

	// Create Zipf distribution
	zipfS := 2.0
	zipfV := 1.0
	// Using standard math/rand for Zipf with local source
	zipf := rand.NewZipf(rand.New(rand.NewSource(time.Now().UnixNano())), zipfS, zipfV, 10000) // N=10000 items

	totalItems := 100000
	gtCounts := make(map[string]int64)

	start := time.Now()
	for i := 0; i < totalItems; i++ {
		num := zipf.Uint64()
		key := fmt.Sprintf("key-%d", num)

		gtCounts[key]++
		us.Update(common.FromString(key), 1)
	}
	duration := time.Since(start)

	fmt.Printf("Processed %d items in %v (%.2f us/op)\n", totalItems, duration, float64(duration.Microseconds())/float64(totalItems))

	// Evaluate Heavy Hitters
	topk := us.QueryTopK(100)

	// Get actual Top 1 item (Most Frequent Item)
	maxKey := ""
	maxVal := int64(0)
	for k, v := range gtCounts {
		if v > maxVal {
			maxVal = v
			maxKey = k
		}
	}

	idx, found := topk.Find(maxKey)
	if assert.True(t, found, "Most frequent item (%s) must be in TopK", maxKey) {
		estVal := topk.Heap[idx].Count
		errRate := math.Abs(float64(estVal-maxVal)) / float64(maxVal)
		fmt.Printf("Top Item '%s': Real=%d, Est=%d, Error=%.2f%%\n", maxKey, maxVal, estVal, errRate*100)
		assert.Less(t, errRate, 0.05, "Error rate for top item must be < 5%")
	}
}
