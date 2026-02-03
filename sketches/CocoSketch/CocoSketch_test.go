package cocosketch

import (
	"math/rand"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCocoSketch_Validation verifies that invalid parameters are rejected.
func TestNewCocoSketch_Validation(t *testing.T) {
	_, err := NewCocoSketch(0, 10)
	require.Error(t, err, "Should fail with d=0")

	_, err = NewCocoSketch(5, 0)
	require.Error(t, err, "Should fail with length=0")

	cs, err := NewCocoSketch(3, 10)
	require.NoError(t, err)
	require.NotNil(t, cs)
	require.Equal(t, "CocoSketch", cs.TypeName())
}

// TestCocoSketch_BasicInsertQuery verifies simple insertion and frequency query.
func TestCocoSketch_BasicInsertQuery(t *testing.T) {
	d, length := 3, 20
	cs, err := NewCocoSketch(d, length)
	require.NoError(t, err)

	// Fix RNG seed for deterministic behavior in test
	cs.rng = rand.New(rand.NewSource(42))

	hash := uint64(0xCAFEBABE)

	// Insert 5 times
	for i := 0; i < 5; i++ {
		cs.InsertWithHash(hash)
	}

	// Query
	// With no collisions (empty sketch), count should be exactly 5
	val, err := cs.QueryWithHash(common.QueryFrequency, hash)
	require.NoError(t, err)
	assert.Equal(t, 5.0, val)

	// Query for non-existent hash
	valMissing, err := cs.QueryWithHash(common.QueryFrequency, 0xDEADBEEF)
	require.NoError(t, err)
	assert.Equal(t, 0.0, valMissing)
}

// TestCocoSketch_AggregationLogic manually sets up bucket states to verify
// how different aggregation strategies combine values.
func TestCocoSketch_AggregationLogic(t *testing.T) {
	d, length := 3, 5
	cs, _ := NewCocoSketch(d, length)

	targetHash := uint64(0x12345678)

	// Get positions for this hash
	pos := cs.positions(targetHash)

	// Manually inject values into buckets to simulate a distributed state
	// Array 0: count = 10
	cs.keys[0][pos[0]] = targetHash
	cs.counts[0][pos[0]] = 10

	// Array 1: count = 20
	cs.keys[1][pos[1]] = targetHash
	cs.counts[1][pos[1]] = 20

	// Array 2: count = 30
	cs.keys[2][pos[2]] = targetHash
	cs.counts[2][pos[2]] = 30

	// 1. Test AggregateRaw (Expects the first value found, usually from array 0)
	// Note: Implementation iterates 0..d-1
	valRaw := cs.QuerySpecific(targetHash, AggregateRaw)
	assert.Equal(t, 10.0, valRaw, "AggregateRaw should return first match (10)")

	// 2. Test AggregateSum (Expects 10 + 20 + 30 = 60)
	valSum := cs.QuerySpecific(targetHash, AggregateSum)
	assert.Equal(t, 60.0, valSum, "AggregateSum should return total (60)")

	// 3. Test AggregateMax (Expects max(10, 20, 30) = 30)
	valMax := cs.QuerySpecific(targetHash, AggregateMax)
	assert.Equal(t, 30.0, valMax, "AggregateMax should return max (30)")

	// 4. Test AggregateMedian (Expects median(10, 20, 30) = 20)
	valMedian := cs.QuerySpecific(targetHash, AggregateMedian)
	assert.Equal(t, 20.0, valMedian, "AggregateMedian should return middle value (20)")
}

// TestCocoSketch_SetAggregation verifies changing the default query strategy.
func TestCocoSketch_SetAggregation(t *testing.T) {
	d, length := 3, 5
	cs, _ := NewCocoSketch(d, length)

	// Default is Median
	assert.Equal(t, AggregateMedian, cs.defaultAgg)

	// Change to Sum
	cs.SetAggregation(AggregateSum)
	assert.Equal(t, AggregateSum, cs.defaultAgg)

	// Setup data for verification
	hash := uint64(1)
	pos := cs.positions(hash)
	// Inject 2 in layer 0, 3 in layer 1
	cs.keys[0][pos[0]] = hash
	cs.counts[0][pos[0]] = 2
	cs.keys[1][pos[1]] = hash
	cs.counts[1][pos[1]] = 3

	// QueryWithHash should now use Sum (2+3 = 5) instead of Median
	val, _ := cs.QueryWithHash(common.QueryFrequency, hash)
	assert.Equal(t, 5.0, val)
}

// TestCocoSketch_Merge verifies merging two sketches.
func TestCocoSketch_Merge(t *testing.T) {
	d, length := 3, 20
	cs1, _ := NewCocoSketch(d, length)
	cs2, _ := NewCocoSketch(d, length)

	// Use deterministic RNG
	cs1.rng = rand.New(rand.NewSource(1))
	cs2.rng = rand.New(rand.NewSource(2))

	hash := uint64(999)

	// cs1: count = 10
	for i := 0; i < 10; i++ {
		cs1.InsertWithHash(hash)
	}

	// cs2: count = 20
	for i := 0; i < 20; i++ {
		cs2.InsertWithHash(hash)
	}

	// Merge cs2 into cs1
	err := cs1.Merge(cs2)
	require.NoError(t, err)

	// Because buckets accumulate counts, if the key is the same, counts should sum up.
	// 10 + 20 = 30
	val, _ := cs1.QueryWithHash(common.QueryFrequency, hash)
	assert.Equal(t, 30.0, val, "Merged count should be sum of parts")

	// Verify mismatched dimensions fail
	badSketch, _ := NewCocoSketch(2, 20) // d mismatch
	err = cs1.Merge(badSketch)
	require.Error(t, err)
}

// TestCocoSketch_Collision verifies behavior when buckets are full.
func TestCocoSketch_Collision(t *testing.T) {
	// Create a tiny sketch (1 bucket) to force collision
	cs, _ := NewCocoSketch(1, 1)
	cs.rng = rand.New(rand.NewSource(1))

	h1 := uint64(100)
	h2 := uint64(200)

	// Insert h1 10 times. Bucket[0] should have key=h1, count=10
	for i := 0; i < 10; i++ {
		cs.InsertWithHash(h1)
	}
	require.Equal(t, h1, cs.keys[0][0])
	require.Equal(t, uint64(10), cs.counts[0][0])

	// Insert h2 once.
	// Logic:
	// 1. Find min bucket (only one choice: index 0).
	// 2. Increment count -> 11.
	// 3. Probabilistic replacement: 1/11 chance to replace key with h2.
	cs.InsertWithHash(h2)

	// Count must increase regardless of key replacement
	assert.Equal(t, uint64(11), cs.counts[0][0])

	// Key is either h1 or h2 (probabilistic)
	isH1 := cs.keys[0][0] == h1
	isH2 := cs.keys[0][0] == h2
	assert.True(t, isH1 || isH2, "Key must be one of the inserted hashes")
}
