package countminsketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// Helper for logging
func logCMS(t *testing.T, name string, expected, estimated float64) {
	t.Helper()
	t.Logf("[%s] expected>=%.2f estimated=%.2f", name, expected, estimated)
}

// ==============================================================================
// 1. STANDARD UNIT TESTS (Existing)
// ==============================================================================

// TestCMS_ZeroState verifies that a newly created Count-Min Sketch returns zero for unseen keys.
func TestCMS_ZeroState(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	h := common.FromString("never").Hash
	est, _ := s.QueryWithHash(common.QueryFrequency, h)

	logCMS(t, "ZeroState", 0, est)
	if est != 0 {
		t.Fatalf("non-zero on empty sketch")
	}
}

// TestCMS_NoUnderestimate validates the core property: estimates >= true count.
func TestCMS_NoUnderestimate(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	for i := 0; i < 500; i++ {
		s.InsertWithHash(common.FromString("hot").Hash)
	}
	est, _ := s.QueryWithHash(common.QueryFrequency, common.FromString("hot").Hash)
	logCMS(t, "NoUnderestimate", 500, est)
	if est < 500 {
		t.Fatalf("CMS underestimation detected")
	}
}

// TestCMS_Linearity checks that updates are additive.
func TestCMS_Linearity(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	for i := 0; i < 200; i++ {
		s.InsertWithHash(common.FromString("x").Hash)
	}
	for i := 0; i < 300; i++ {
		s.InsertWithHash(common.FromString("x").Hash)
	}
	est, _ := s.QueryWithHash(common.QueryFrequency, common.FromString("x").Hash)
	logCMS(t, "Linearity", 500, est)
	if est < 500 {
		t.Fatalf("linearity violated")
	}
}

// TestCMS_Monotonicity ensures estimates never decrease.
func TestCMS_Monotonicity(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	h := common.FromString("mono").Hash
	prev := 0.0
	for i := 0; i < 10; i++ {
		s.InsertWithHash(h)
		cur, _ := s.QueryWithHash(common.QueryFrequency, h)
		if cur < prev {
			t.Fatalf("count decreased")
		}
		prev = cur
	}
}

// TestCMS_Determinism verifies identical inputs yield identical results.
func TestCMS_Determinism(t *testing.T) {
	s1, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	s2, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	keys := []string{"a", "a", "b", "c", "c", "c"}

	for _, k := range keys {
		h := common.FromString(k).Hash
		s1.InsertWithHash(h)
		s2.InsertWithHash(h)
	}

	for _, k := range []string{"a", "b", "c"} {
		h := common.FromString(k).Hash
		c1, _ := s1.QueryWithHash(common.QueryFrequency, h)
		c2, _ := s2.QueryWithHash(common.QueryFrequency, h)
		if c1 != c2 {
			t.Fatalf("non-deterministic result")
		}
	}
}

// TestCMS_MergeCorrectness ensures merging preserves no-underestimation.
func TestCMS_MergeCorrectness(t *testing.T) {
	s1, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	s2, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	for i := 0; i < 400; i++ {
		s1.InsertWithHash(common.FromString("m").Hash)
	}
	for i := 0; i < 600; i++ {
		s2.InsertWithHash(common.FromString("m").Hash)
	}
	if err := s1.Merge(s2); err != nil {
		t.Fatalf("merge failed")
	}
	est, _ := s1.QueryWithHash(common.QueryFrequency, common.FromString("m").Hash)
	logCMS(t, "Merge", 1000, est)
	if est < 1000 {
		t.Fatalf("merge underestimation")
	}
}

// TestCMS_L1L2 validates internal consistency (norms).
func TestCMS_L1L2(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	N := 300
	for i := 0; i < N; i++ {
		s.InsertWithHash(common.FromString(fmt.Sprintf("k%d", i%10)).Hash)
	}
	l1 := s.CM_L1()
	l2 := s.CM_L2()
	if math.Abs(l1-float64(N)) > 1e-9 {
		t.Fatalf("L1 incorrect")
	}
	if l2 < math.Sqrt(float64(N)) || l2 > float64(N) {
		t.Fatalf("L2 out of bounds")
	}
}

// TestCMS_QueryNoSideEffect ensures queries are read-only.
func TestCMS_QueryNoSideEffect(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	h := common.FromString("pure").Hash
	s.InsertWithHash(h)
	before, _ := s.QueryWithHash(common.QueryFrequency, h)
	for i := 0; i < 100; i++ {
		s.QueryWithHash(common.QueryFrequency, h)
	}
	after, _ := s.QueryWithHash(common.QueryFrequency, h)
	if before != after {
		t.Fatalf("query mutated state")
	}
}

func TestCMS_RustStyleAPI(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if s.RowCount() != DefaultRowNum || s.ColCount() != DefaultColNum {
		t.Fatalf("unexpected default dimensions: got (%d,%d)", s.RowCount(), s.ColCount())
	}

	input := common.FromString("rust-api")
	s.Insert(input)
	s.InsertMany(input, 4)

	if got := s.Estimate(input); got < 5 {
		t.Fatalf("estimate too small: got %v", got)
	}
}

// ==============================================================================
// 2. STATISTICAL & REAL-WORLD TESTS (New)
// ==============================================================================

// TestCMS_Statistical_Zipf verifies the (epsilon, delta) guarantee on skewed data.
func TestCMS_Statistical_Zipf(t *testing.T) {
	// 1. Setup
	rows, cols := 5, 2048
	totalItems := 100_000
	cms, _ := NewCountMinSketch(rows, cols)
	groundTruth := make(map[uint64]int64)

	// 2. Generate Zipfian Data
	src := rand.NewSource(42)
	rng := rand.New(src)
	zipf := rand.NewZipf(rng, 1.1, 1.0, 8192)

	for i := 0; i < totalItems; i++ {
		key := zipf.Uint64()
		groundTruth[key]++

		// Convert to bytes for hashing
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], key)
		cms.InsertWithHash(common.Hash64(b[:]))
	}

	// 3. Verify Guarantees
	epsilon := math.E / float64(cols)
	delta := 1.0 / math.Pow(math.E, float64(rows))
	errorBound := epsilon * float64(totalItems)

	validCount := 0
	for key, trueCount := range groundTruth {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], key)
		est, _ := cms.QueryWithHash(common.QueryFrequency, common.Hash64(b[:]))

		if est < float64(trueCount) {
			t.Fatalf("Underestimation detected for key %d", key)
		}

		if math.Abs(est-float64(trueCount)) <= errorBound {
			validCount++
		}
	}

	requiredPassing := float64(len(groundTruth)) * (1.0 - delta)
	t.Logf("[Stat-Zipf] Passing: %d/%d (Required > %.0f)", validCount, len(groundTruth), requiredPassing)

	if float64(validCount) < requiredPassing {
		t.Errorf("Statistical guarantee failed. Too many errors outside bound.")
	}
}

// TestCMS_RealWorld_CAIDA_Accuracy runs a full accuracy check using the provided PCAP files.
func TestCMS_RealWorld_CAIDA_Accuracy(t *testing.T) {
	// 1. Load CAIDA Data (Adjust path relative to your test file location)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	// ReadCAIDAStream assumes the testdata package is accessible
	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Skip("No CAIDA samples found.")
	}

	// 2. Initialize
	rows, cols := 5, 2048 // Standard configuration
	cms, _ := NewCountMinSketch(rows, cols)
	groundTruth := make(map[uint32]int64)

	// 3. Process Stream
	for _, s := range samples {
		ip := uint32(s.F)
		groundTruth[ip]++

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ip)
		cms.InsertWithHash(common.Hash64(ipBytes))
	}

	// 4. Analyze Top-100 Heavy Hitters
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
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, item.IP)

		est, _ := cms.QueryWithHash(common.QueryFrequency, common.Hash64(ipBytes))

		// Check 1: No Underestimation
		if est < float64(item.Count) {
			t.Fatalf("CRITICAL: Underestimation on IP %d. Est: %.0f, True: %d", item.IP, est, item.Count)
		}

		// Accumulate Error
		totalRelErr += (est - float64(item.Count)) / float64(item.Count)
	}

	avgRelError := (totalRelErr / float64(topK)) * 100
	t.Logf("[CAIDA-Accuracy] Processed %d packets. Unique IPs: %d", len(samples), len(groundTruth))
	t.Logf("[CAIDA-Accuracy] Top-%d Avg Relative Error: %.4f%%", topK, avgRelError)

	// Threshold: Real-world heavy hitters should have low error (< 15%) in this config
	if avgRelError > 15.0 {
		t.Errorf("Accuracy too low on real-world data: %.2f%%", avgRelError)
	}
}

func TestCountMinSketchSerializeRoundTrip(t *testing.T) {
	s, err := NewCountMinSketch(3, 1024)
	if err != nil {
		t.Fatalf("new countmin: %v", err)
	}

	for i := 0; i < 100; i++ {
		s.InsertWithHash(common.FromString("hot-key").Hash)
	}

	before, err := s.QueryWithHash(common.QueryFrequency, common.FromString("hot-key").Hash)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}

	data, err := s.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	restored, err := DeserializeCountMinSketchFromBytes(data)
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
}

// TestCMS_Merge_ElementWise verifies that merging sums every counter individually.
func TestCMS_Merge_ElementWise(t *testing.T) {
	rows, cols := 2, 16
	s1, _ := NewCountMinSketch(rows, cols)
	s2, _ := NewCountMinSketch(rows, cols)

	// Insert specific known value
	h := common.FromString("test").Hash
	s1.InsertWithHash(h)
	s2.InsertWithHash(h)
	s2.InsertWithHash(h) // s1 has 1, s2 has 2

	// Capture state before merge
	s1Old := make([][]float64, rows)
	s2Old := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		s1Old[r] = make([]float64, cols)
		s2Old[r] = make([]float64, cols)
		copy(s1Old[r], s1.Count[r])
		copy(s2Old[r], s2.Count[r])
	}

	if err := s1.Merge(s2); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	// Verify every single cell is s1_old + s2_old
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			expected := s1Old[r][c] + s2Old[r][c]
			actual := s1.Count[r][c]
			if actual != expected {
				t.Errorf("Merge mismatch at [%d][%d]: %.0f != %.0f", r, c, actual, expected)
			}
		}
	}

	// High level check
	est, _ := s1.QueryWithHash(common.QueryFrequency, h)
	if est != 3.0 {
		t.Errorf("Expected merged count 3.0, got %.1f", est)
	}
}

func TestCountMinSketch_ErrorBound_CAIDA(t *testing.T) {
	// 1. Load CAIDA Data
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA Error Bound test: %v", err)
	}
	if len(samples) == 0 {
		t.Skip("No samples found in CAIDA file")
	}

	// 2. Configuration
	rows, cols := 5, 2048
	totalItems := len(samples) // Total number of packets

	cms, _ := NewCountMinSketch(rows, cols)
	groundTruth := make(map[uint32]int64)

	// 3. Ingest Data
	for _, s := range samples {
		ip := uint32(s.F)
		groundTruth[ip]++

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ip)
		cms.InsertWithHash(common.Hash64(ipBytes))
	}

	// 4. Calculate Theoretical Bounds
	// epsilon = e / width (The error factor)
	epsilon := math.E / float64(cols)

	// delta = 1 / e^depth (The failure probability)
	delta := 1.0 / math.Pow(math.E, float64(rows))

	// The error bound is epsilon * N (total count)
	// For CMS, the estimate is <= true_count + (epsilon * N)
	errorBound := epsilon * float64(totalItems)

	// We expect (1-delta) of queries to be within the error bound
	correctLowerBound := float64(len(groundTruth)) * (1.0 - delta)

	// 5. Verify Estimates
	withinCount := 0
	for ip, trueCount := range groundTruth {
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ip)

		est, _ := cms.QueryWithHash(common.QueryFrequency, common.Hash64(ipBytes))

		// Check the difference
		diff := math.Abs(est - float64(trueCount))

		if diff <= errorBound {
			withinCount++
		}
	}

	// 6. Report and Assert
	t.Log("===================================================")
	t.Logf(" CAIDA ERROR BOUND ANALYSIS")
	t.Log("===================================================")
	t.Logf(" Total Packets (N): %d", totalItems)
	t.Logf(" Unique Flows:      %d", len(groundTruth))
	t.Logf(" Sketch Size:       %d x %d", rows, cols)
	t.Logf(" Theoretical Epsilon: %.5f", epsilon)
	t.Logf(" Max Allowed Error:   %.2f", errorBound)
	t.Logf(" Confidence (1-delta): %.4f%%", (1.0-delta)*100)
	t.Log("---------------------------------------------------")
	t.Logf(" Flows within bound:  %d / %d", withinCount, len(groundTruth))
	t.Logf(" Success Rate:        %.4f%%", (float64(withinCount)/float64(len(groundTruth)))*100)
	t.Log("===================================================")

	if float64(withinCount) <= correctLowerBound {
		t.Errorf("Statistical guarantee failed on CAIDA data.\n"+
			"Expected at least %.0f flows within error bound, but got %d.\n"+
			"This might indicate the sketch width (%d) is too small for this trace's entropy.",
			correctLowerBound, withinCount, cols)
	}
}
