package cocosketch

import (
	"encoding/binary"
	"math"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to load CAIDA data
func loadCAIDA(t *testing.T) []testdata.Sample {
	// Adjust path relative to this file
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Skip("No CAIDA samples found.")
	}
	return samples
}

// TestNewCocoSketch_Validation verifies that invalid parameters are rejected.
// (Kept as logic check, data independent)
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

// TestCocoSketch_CAIDA_BasicInsertQuery verifies that we can ingest the CAIDA stream
// and retrieve a non-zero count for a known heavy hitter.
func TestCocoSketch_CAIDA_BasicInsertQuery(t *testing.T) {
	samples := loadCAIDA(t)

	// Initialize: 4 arrays, 4096 buckets each
	d, length := 4, 4096
	cs, err := NewCocoSketch(d, length)
	require.NoError(t, err)

	// Identify a heavy hitter from the first 1000 packets to query later
	targetIP := uint32(samples[0].F)
	targetHash := common.Hash64(ipToBytes(targetIP))

	// Ingest Data
	for _, s := range samples {
		ipBytes := ipToBytes(uint32(s.F))
		cs.InsertWithHash(common.Hash64(ipBytes))
	}

	// Query the known existing key
	val, err := cs.QueryWithHash(common.QueryFrequency, targetHash)
	require.NoError(t, err)
	assert.Greater(t, val, 0.0, "Heavy hitter should have non-zero count")

	// Query a non-existent key (0xDEADBEEF)
	valMissing, err := cs.QueryWithHash(common.QueryFrequency, 0xDEADBEEF)
	require.NoError(t, err)

	// CocoSketch is probabilistic; it *might* return non-zero noise,
	// but usually 0 for completely random unused keys in a sparse sketch.
	// We just ensure it doesn't error.
	assert.GreaterOrEqual(t, valMissing, 0.0)
}

// TestCocoSketch_CAIDA_Merge splits the CAIDA stream in two, sketches them separately,
// merges them, and verifies the count matches a sketch of the full stream.
func TestCocoSketch_CAIDA_Merge(t *testing.T) {
	samples := loadCAIDA(t)
	mid := len(samples) / 2

	d, length := 4, 4096
	csPart1, _ := NewCocoSketch(d, length)
	csPart2, _ := NewCocoSketch(d, length)
	csTotal, _ := NewCocoSketch(d, length)

	// Ingest Data
	for i, s := range samples {
		hash := common.Hash64(ipToBytes(uint32(s.F)))

		// Insert into Total
		csTotal.InsertWithHash(hash)

		// Insert into Parts
		if i < mid {
			csPart1.InsertWithHash(hash)
		} else {
			csPart2.InsertWithHash(hash)
		}
	}

	// Merge Part2 into Part1
	err := csPart1.Merge(csPart2)
	require.NoError(t, err)

	// Verification: Check Top-10 Heavy Hitters from Ground Truth
	// Note: CocoSketch is probabilistic (random replacement), so exact matches
	// between csTotal and csMerged are unlikely because RNG calls will differ.
	// Instead, we verify that the merged sketch accurately estimates the total count.

	// Build Ground Truth for check
	groundTruth := make(map[uint64]int64)
	for _, s := range samples {
		hash := common.Hash64(ipToBytes(uint32(s.F)))
		groundTruth[hash]++
	}

	// Check one heavy hitter
	var heavyKey uint64
	var maxCount int64
	for k, v := range groundTruth {
		if v > maxCount {
			maxCount = v
			heavyKey = k
		}
	}

	estMerged, _ := csPart1.QueryWithHash(common.QueryFrequency, heavyKey)

	// Allow 20% error margin for this probabilistic data structure
	errorMargin := float64(maxCount) * 0.20
	assert.InDelta(t, float64(maxCount), estMerged, errorMargin,
		"Merged sketch should approximate true count within margin")
}

// TestCocoSketch_CAIDA_AggregationStrategies verifies how different aggregation
// methods (Median, Max, Sum) behave on real data.
func TestCocoSketch_CAIDA_AggregationStrategies(t *testing.T) {
	samples := loadCAIDA(t)
	cs, _ := NewCocoSketch(5, 2048)

	for _, s := range samples {
		cs.InsertWithHash(common.Hash64(ipToBytes(uint32(s.F))))
	}

	// Find the heaviest item
	groundTruth := make(map[uint64]int64)
	for _, s := range samples {
		groundTruth[common.Hash64(ipToBytes(uint32(s.F)))]++
	}
	var heavyKey uint64
	var maxCount int64
	for k, v := range groundTruth {
		if v > maxCount {
			maxCount = v
			heavyKey = k
		}
	}

	// Query with different strategies
	valMedian := cs.QuerySpecific(heavyKey, AggregateMedian)
	valMax := cs.QuerySpecific(heavyKey, AggregateMax)
	valSum := cs.QuerySpecific(heavyKey, AggregateSum)

	t.Logf("Key %x (True: %d) -> Median: %.0f, Max: %.0f, Sum: %.0f",
		heavyKey, maxCount, valMedian, valMax, valSum)

	// In a single un-merged sketch, CocoSketch usually keeps a key in only one bucket.
	// Therefore, Sum, Max, and Median should be roughly identical.
	// However, if collisions forced the key to be evicted and re-inserted elsewhere,
	// or if we had a merge, they might differ.

	// For standard Cornucopia logic, we expect them to be close.
	assert.InDelta(t, valMax, valMedian, float64(maxCount)*0.1, "Median and Max should be close")
}

// TestCocoSketch_CAIDA_Accuracy calculates the relative error for Top-K items.
func TestCocoSketch_CAIDA_Accuracy(t *testing.T) {
	samples := loadCAIDA(t)

	// Configuration
	// d=4 arrays, length=4096 buckets (Total 16k buckets)
	d, length := 4, 4096
	cs, _ := NewCocoSketch(d, length)

	groundTruth := make(map[uint32]int64)

	// Ingest
	for _, s := range samples {
		ip := uint32(s.F)
		groundTruth[ip]++
		cs.InsertWithHash(common.Hash64(ipToBytes(ip)))
	}

	// Sort Ground Truth to get Top-K
	type kv struct {
		IP    uint32
		Count int64
	}
	var sorted []kv
	for k, v := range groundTruth {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Count > sorted[j].Count
	})

	topK := 100
	if len(sorted) < topK {
		topK = len(sorted)
	}

	var totalRelErr float64
	for i := 0; i < topK; i++ {
		item := sorted[i]
		hash := common.Hash64(ipToBytes(item.IP))

		est, _ := cs.QueryWithHash(common.QueryFrequency, hash)

		// CocoSketch can underestimate if the item was evicted and never replaced
		// It generally doesn't overestimate significantly unless collisions are high
		err := math.Abs(est - float64(item.Count))
		relErr := err / float64(item.Count)
		totalRelErr += relErr
	}

	avgRelError := (totalRelErr / float64(topK)) * 100

	t.Log("===================================================")
	t.Logf(" CAIDA ACCURACY REPORT (CocoSketch)")
	t.Logf(" Processed: %d packets, Unique IPs: %d", len(samples), len(groundTruth))
	t.Logf(" Top-%d Avg Relative Error: %.4f%%", topK, avgRelError)
	t.Log("===================================================")

	// Threshold: CocoSketch (Cornucopia) is designed for high accuracy on heavy hitters.
	// We expect reasonable performance (< 15% error).
	if avgRelError > 15.0 {
		t.Errorf("Accuracy too low on real-world data: %.2f%%", avgRelError)
	}
}

// Helper: Convert uint32 IP to 4-byte slice
func ipToBytes(ip uint32) []byte {
	bytes := make([]byte, 4)
	binary.BigEndian.PutUint32(bytes, ip)
	return bytes
}
