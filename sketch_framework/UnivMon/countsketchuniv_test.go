package univmon

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCountSketchUniv_Basic verifies basic operations: Create, Update, Query, Clean
func TestCountSketchUniv_Basic(t *testing.T) {
	row := CS_ROW_NO_Univ_ELEPHANT
	col := CS_COL_NO_Univ_ELEPHANT

	// 1. Creation
	cs, err := NewCountSketchUniv(row, col)
	require.NoError(t, err)
	require.NotNil(t, cs)

	// Verify zero initialization
	for r := 0; r < cs.row; r++ {
		for c := 0; c < cs.col; c++ {
			require.Equal(t, int64(0), cs.count[r][c])
		}
	}

	// 2. Data Preparation
	cases := []struct {
		key string
		cnt int64
	}{
		{"apple", 1},
		{"banana", 10},
		{"apple", 2}, // Total apple = 3
		{"orange", 5},
	}

	// 3. Update Loop
	for _, c := range cases {
		input := common.FromString(c.key)
		// Using UpdateWithHash (can also use InsertWithHash for count=1)
		cs.UpdateWithHash(input.Hash, c.cnt)
	}

	// 4. Query & Verification
	// Apple = 3
	estApple, _ := cs.QueryWithHash(common.QueryFrequency, common.FromString("apple").Hash)
	assert.Equal(t, 3.0, estApple, "Frequency estimation for 'apple' is incorrect")

	// Banana = 10
	estBanana, _ := cs.QueryWithHash(common.QueryFrequency, common.FromString("banana").Hash)
	assert.Equal(t, 10.0, estBanana, "Frequency estimation for 'banana' is incorrect")

	// Orange = 5
	estOrange, _ := cs.QueryWithHash(common.QueryFrequency, common.FromString("orange").Hash)
	assert.Equal(t, 5.0, estOrange, "Frequency estimation for 'orange' is incorrect")

	// Not Found = 0
	estGhost, _ := cs.QueryWithHash(common.QueryFrequency, common.FromString("ghost").Hash)
	assert.Equal(t, 0.0, estGhost, "Non-existent item must be 0 (in case of no collision)")

	// 5. Test Clean
	err = cs.CleanCountSketchUniv()
	require.NoError(t, err)
	estAppleAfterClean, _ := cs.QueryWithHash(common.QueryFrequency, common.FromString("apple").Hash)
	assert.Equal(t, 0.0, estAppleAfterClean, "Sketch must be empty after Clean")
}

// TestCSL2 tests L2 Norm estimation accuracy (Second Frequency Moment)
// Adapted from your original test to match new API
func TestCSL2(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	// Setup Sketch
	row := CS_ROW_NO_Univ_ELEPHANT
	col := CS_COL_NO_Univ_ELEPHANT

	t_now := time.Now()
	cs, err := NewCountSketchUniv(row, col)
	require.NoError(t, err)
	t.Log("Setup time:", time.Since(t_now))

	// Setup Synthetic Data (Zipf)
	s_zipf := 2.0
	v_zipf := 1.0
	value_scale_local := 50000 // Adjust scale

	zipf := rand.NewZipf(rand.New(rand.NewSource(time.Now().Unix())), s_zipf, v_zipf, uint64(value_scale_local))

	// Ground Truth Calculation
	l1Map := make(map[float64]float64)
	t2 := 100000 // Number of items

	// Generate & Update Sketch via streaming
	start := time.Now()
	for i := 0; i < t2; i++ {
		val := float64(zipf.Uint64())

		// Update Ground Truth
		l1Map[val]++

		// Update Sketch
		// Convert float to string key to be consistent with old method,
		// or use common.FromF64(val) if you want to hash raw float bytes.
		// Here we follow the old pattern: strconv key
		key := strconv.FormatFloat(val, 'f', -1, 64)
		input := common.FromString(key)

		cs.UpdateWithHash(input.Hash, 1)
	}
	t.Log("Update time:", time.Since(start))

	// Calculate Exact L2
	var l2_exact_sq float64 = 0.0
	for _, count := range l1Map {
		l2_exact_sq += count * count
	}
	l2_exact := math.Sqrt(l2_exact_sq)

	// Query Sketch L2
	// Using QueryWithHash with QuerySum2 type
	l2_est, _ := cs.QueryWithHash(common.QuerySum2, 0) // Hash argument ignored for L2 query

	// Evaluate Error
	fmt.Printf("Exact L2: %.4f, Est L2: %.4f\n", l2_exact, l2_est)

	l2_err := math.Abs(l2_est-l2_exact) / l2_exact
	fmt.Printf("L2 Error Rate: %.4f%%\n", l2_err*100)

	// Assert reasonable error rate (e.g. < 5% for this setting)
	assert.Less(t, l2_err, 0.05, "L2 Error rate too high")
}

// TestCountSketchUniv_Merge verifies Merge function
func TestCountSketchUniv_Merge(t *testing.T) {
	row, col := 5, 1024
	cs1, _ := NewCountSketchUniv(row, col)
	cs2, _ := NewCountSketchUniv(row, col)

	key := "test_merge"
	input := common.FromString(key)

	// CS1: count = 10
	cs1.UpdateWithHash(input.Hash, 10)

	// CS2: count = 20
	cs2.UpdateWithHash(input.Hash, 20)

	// Merge CS2 into CS1
	err := cs1.Merge(cs2)
	require.NoError(t, err)

	// CS1 should now be 30
	est, _ := cs1.QueryWithHash(common.QueryFrequency, input.Hash)
	assert.Equal(t, 30.0, est, "Merge failed to sum counters")
}

// TestUpdateAndEstimate verifies combined Update+Estimate function (UnivMon core)
func TestUpdateAndEstimate(t *testing.T) {
	cs, _ := NewCountSketchUniv(5, 1024)
	input := common.FromString("itemX")

	// Update 1: return value must be new estimate (1)
	est1 := cs.UpdateAndEstimateHash(input.Hash, 1)
	assert.Equal(t, int64(1), est1)

	// Update 2: return value must be new estimate (1+2=3)
	est2 := cs.UpdateAndEstimateHash(input.Hash, 2)
	assert.Equal(t, int64(3), est2)
}
