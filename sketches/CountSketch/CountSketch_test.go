package countsketch

import (
	"encoding/binary"
	"math"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// Helper to load CAIDA data for tests
func loadCAIDA(t *testing.T) []testdata.Sample {
	// Adjust path as needed for your project structure
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

// ==============================================================================
// 1. BASIC PROPERTY TESTS (Synthetic Data)
// ==============================================================================

// TestCS_ZeroState verifies initialization.
func TestCS_ZeroState(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)
	est := float64(cs.EstimateStringCount("never_seen"))
	if math.Abs(est) > 0 {
		t.Fatalf("zero-state incorrect")
	}
}

// TestCS_SingleKeyCorrectness checks basic insertion.
func TestCS_SingleKeyCorrectness(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)
	for i := 0; i < 1000; i++ {
		cs.UpdateString("key", 1)
	}
	est := float64(cs.EstimateStringCount("key"))
	if math.Abs(est-1000) > 2 {
		t.Fatalf("single-key incorrect")
	}
}

// TestCS_MedianEstimator ensures robustness against outliers.
func TestCS_MedianEstimator(t *testing.T) {
	cs, _ := NewCountSketch(3, 1024)
	hash := common.Hash64([]byte("k"))

	// Poison one row to simulate a massive collision
	c, sign := cs.derivePosAndSign(hash, 0)
	cs.Count[0][c] += 10_000 * float64(sign)

	for i := 0; i < 3; i++ {
		cs.InsertWithHash(hash)
	}

	est, _ := cs.QueryWithHash(common.QueryFrequency, hash)

	// The median should ignore the 10,000 outlier
	if est < 2 || est > 4 {
		t.Fatalf("median estimator broken: got %.2f", est)
	}
}

// TestCS_SignCorrectness verifies cancellation mechanism.
func TestCS_SignCorrectness(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)
	for i := 0; i < 100; i++ {
		cs.UpdateString("pos", 1)
		cs.UpdateString("neg", -1)
	}
	estPos := float64(cs.EstimateStringCount("pos"))
	estNeg := float64(cs.EstimateStringCount("neg"))

	if estPos <= 0 || estNeg >= 0 {
		t.Fatalf("sign incorrect")
	}
}

// ==============================================================================
// 2. LOGIC TESTS USING CAIDA DATASET
// ==============================================================================

// TestCS_CAIDA_MergeCorrectness verifies that splitting the CAIDA dataset into two
// parts and merging the resulting sketches yields the same state as processing the full stream.
func TestCS_CAIDA_MergeCorrectness(t *testing.T) {
	samples := loadCAIDA(t)
	mid := len(samples) / 2
	rows, cols := 5, 2048

	// 1. Create Partial Sketches
	csPart1, _ := NewCountSketch(rows, cols)
	csPart2, _ := NewCountSketch(rows, cols)

	// 2. Create Total Sketch
	csTotal, _ := NewCountSketch(rows, cols)

	// Ingest Data
	for i, s := range samples {
		ipUint := uint32(s.F)
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ipUint)
		h := common.Hash64(ipBytes)

		// Insert into Total
		csTotal.InsertWithHash(h)

		// Insert into Parts
		if i < mid {
			csPart1.InsertWithHash(h)
		} else {
			csPart2.InsertWithHash(h)
		}
	}

	// 3. Merge Parts
	if err := csPart1.Merge(csPart2); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	// 4. Verify Internal State (Exact Match)
	// Since floating point addition of 1.0 is associative for these ranges,
	// the matrices should be identical.
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			valMerged := csPart1.Count[r][c]
			valTotal := csTotal.Count[r][c]
			if valMerged != valTotal {
				t.Errorf("Merge mismatch at [%d][%d]: Merged=%.0f, Total=%.0f", r, c, valMerged, valTotal)
				return // Fail fast
			}
		}
	}
	t.Logf("Merge Correctness Verified on %d packets", len(samples))
}

// TestCS_CAIDA_OrderIndependence verifies that processing the CAIDA stream
// forwards vs backwards results in the same sketch state (Commutativity).
func TestCS_CAIDA_OrderIndependence(t *testing.T) {
	samples := loadCAIDA(t)
	rows, cols := 5, 2048

	csForward, _ := NewCountSketch(rows, cols)
	csBackward, _ := NewCountSketch(rows, cols)

	// Forward
	for _, s := range samples {
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, uint32(s.F))
		csForward.InsertWithHash(common.Hash64(ipBytes))
	}

	// Backward
	for i := len(samples) - 1; i >= 0; i-- {
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, uint32(samples[i].F))
		csBackward.InsertWithHash(common.Hash64(ipBytes))
	}

	// Compare
	mismatch := false
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if csForward.Count[r][c] != csBackward.Count[r][c] {
				mismatch = true
				break
			}
		}
	}

	if mismatch {
		t.Fatal("Order Independence violated on CAIDA dataset")
	}
	t.Logf("Order Independence Verified on %d packets", len(samples))
}

// TestCS_CAIDA_Accuracy validates the median estimator against ground truth.
func TestCS_CAIDA_Accuracy(t *testing.T) {
	samples := loadCAIDA(t)

	// Initialize Sketch
	rows, cols := 5, 2048
	cs, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("Failed to initialize CountSketch: %v", err)
	}

	groundTruth := make(map[uint32]int64)

	// Ingest Stream
	for _, s := range samples {
		ip := uint32(s.F)
		groundTruth[ip]++

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ip)
		cs.InsertWithHash(common.Hash64(ipBytes))
	}

	// Sort Ground Truth
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

	// Verify Estimates
	var totalRelErr float64
	for i := 0; i < topK; i++ {
		item := sorted[i]
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, item.IP)

		est, _ := cs.QueryWithHash(common.QueryFrequency, common.Hash64(ipBytes))

		err := math.Abs(est - float64(item.Count))
		relErr := err / float64(item.Count)
		totalRelErr += relErr
	}

	avgRelError := (totalRelErr / float64(topK)) * 100

	t.Log("===================================================")
	t.Logf(" CAIDA ACCURACY REPORT (CountSketch)")
	t.Logf(" Processed: %d packets, Unique IPs: %d", len(samples), len(groundTruth))
	t.Logf(" Top-%d Avg Relative Error: %.4f%%", topK, avgRelError)
	t.Log("===================================================")

	if avgRelError > 20.0 {
		t.Errorf("Accuracy too low on real-world data: %.2f%%", avgRelError)
	}
}

func TestCountSketchSerializeRoundTrip(t *testing.T) {
	cs, err := NewCountSketch(5, 1024)
	if err != nil {
		t.Fatalf("new countsketch: %v", err)
	}

	for i := 0; i < 200; i++ {
		cs.UpdateString("hot-key", 1)
	}

	before, err := cs.QueryWithHash(common.QueryFrequency, common.FromString("hot-key").Hash)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}

	data, err := cs.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	restored, err := DeserializeCountSketchFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	after, err := restored.QueryWithHash(common.QueryFrequency, common.FromString("hot-key").Hash)
	if err != nil {
		t.Fatalf("query after: %v", err)
	}

	if before != after {
		t.Fatalf("round-trip mismatch: before=%v after=%v", before, after)
	}

	if restored.TopK == nil {
		t.Fatal("restored TopK should not be nil")
	}
}
