package univmon

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =====================================================
// HELPER: Load CAIDA Data (Local to this file)
// =====================================================

func loadCaidaCS(t *testing.T) ([]string, int) {
	// Adjust path relative to where test is run (sketch_framework/UnivMon)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("No samples loaded from CAIDA files")
	}

	keys := make([]string, len(samples))
	for i, s := range samples {
		// Use raw IP bytes as unique key source (formatted as hex string for consistency)
		ipUint := uint32(s.F)
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ipUint)
		keys[i] = fmt.Sprintf("%x", ipBytes)
	}

	return keys, len(samples)
}

// =====================================================
// UNIT TESTS
// =====================================================

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

// =====================================================
// CAIDA REAL-WORLD L2 ACCURACY TEST
// =====================================================

// TestCountSketchUniv_L2_CAIDA tests L2 Norm (Second Frequency Moment) estimation
func TestCountSketchUniv_L2_CAIDA(t *testing.T) {
	// 1. Setup Sketch
	row := CS_ROW_NO_Univ_ELEPHANT
	col := CS_COL_NO_Univ_ELEPHANT // Ensure this is large enough (e.g., 2048 or 4096) for good L2 accuracy

	cs, err := NewCountSketchUniv(row, col)
	require.NoError(t, err)

	// 2. Load CAIDA Data
	keys, n := loadCaidaCS(t)
	t.Logf("Processing %d packets for L2 estimation...", n)

	// 3. Process Stream & Compute Ground Truth
	gtCounts := make(map[string]int64)

	start := time.Now()
	for _, key := range keys {
		// Ground Truth
		gtCounts[key]++

		// Update Sketch
		input := common.FromString(key)
		cs.UpdateWithHash(input.Hash, 1)
	}
	duration := time.Since(start)
	t.Logf("Updates completed in %v", duration)

	// 4. Calculate Exact L2
	var l2SumSq float64
	for _, count := range gtCounts {
		l2SumSq += float64(count * count)
	}
	l2Exact := math.Sqrt(l2SumSq)

	// 5. Query Sketch L2
	// FIX: The sketch implementation of QuerySum2 ALREADY returns the L2 Norm (Sqrt),
	// so we do NOT need to Sqrt it again.
	l2Est, _ := cs.QueryWithHash(common.QuerySum2, 0)

	// 6. Evaluate Error
	l2Err := math.Abs(l2Est-l2Exact) / l2Exact

	t.Logf("Exact L2: %.4f", l2Exact)
	t.Logf("Est L2:   %.4f", l2Est)
	t.Logf("Error:    %.4f%%", l2Err*100)

	// Assert reasonable error rate (e.g. < 5%)
	if l2Err > 0.05 {
		t.Errorf("L2 Error rate too high: %.2f%% > 5%%", l2Err*100)
	} else {
		t.Log("L2 Accuracy Test Passed")
	}
}
