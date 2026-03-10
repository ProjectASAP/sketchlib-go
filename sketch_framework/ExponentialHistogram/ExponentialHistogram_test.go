package exponentialhistogram

import (
	"math"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// TestEH_CAIDA_Stress runs a full benchmark and correctness test using the CAIDA dataset.
// It verifies:
// 1. Throughput (Efficiency of Incremental L2)
// 2. Hybrid Transition (Map -> Sketch promotion)
// 3. Accuracy (L2 Norm vs Ground Truth)
func TestEH_CAIDA_Stress(t *testing.T) {
	// 1. Configuration
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	file2 := "../../testdata/caida/equinix-nyc.dirA.20181220-130300.UTC.anon.pcap.gz"

	// 2. Load Data
	t.Log("Loading CAIDA stream...")
	stream, err := testdata.ReadCAIDAStream(file1, file2)
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
		return
	}
	t.Logf("Loaded %d packets.", len(stream))

	// 3. Initialize Exponential Histogram
	//    k=50, Window=100,000 packets
	//    UnivMon: Row=5, Col=200 (Increased Col for better accuracy in this test)
	k := 50
	windowSize := int64(100000)
	// Increasing Col to 200 to ensure we hit a tighter error bound (~10%)
	eh := NewExpoHistogramUniv(k, windowSize, 50, 5, 200, 3)

	// 4. Processing Loop (Benchmark)
	start := time.Now()
	for i, packet := range stream {
		ipKey := strconv.FormatUint(uint64(packet.F), 10)
		if err := eh.UpdateItem(ipKey, packet.T); err != nil {
			t.Fatalf("Update failed at packet %d: %v", i, err)
		}
	}
	duration := time.Since(start)

	t.Logf("--- Benchmark Results ---")
	t.Logf("Throughput:    %.2f packets/sec", float64(len(stream))/duration.Seconds())
	t.Logf("Total Time:    %v", duration)

	// 5. Verify Hybrid Behavior
	checkHybridBehavior(t, eh)

	// 6. Verify Correctness & Error Bounds
	//    We verify the *last active window* of the stream.
	lastT := stream[len(stream)-1].T
	startT := lastT - windowSize
	if startT < 0 {
		startT = 0
	}

	checkCorrectness(t, eh, stream, startT, lastT)
}

// checkHybridBehavior confirms Map -> Sketch promotion occurred
func checkHybridBehavior(t *testing.T, eh *ExpoHistogramUniv) {
	mapCount, sketchCount := 0, 0
	eh.mu.RLock()
	defer eh.mu.RUnlock()

	for _, bucket := range eh.buckets {
		hs, ok := bucket.Sketch.(*HybridSketch)
		if !ok {
			continue
		}
		if hs.isSketch {
			sketchCount++
		} else {
			mapCount++
		}
	}

	t.Logf("Structure State: %d Exact Maps, %d Promoted Sketches", mapCount, sketchCount)
	if sketchCount == 0 {
		t.Error("FAIL: No buckets promoted to Sketch mode. Memory Trigger likely failed.")
	}
}

// checkCorrectness calculates Ground Truth for the window and compares with Sketch
func checkCorrectness(t *testing.T, eh *ExpoHistogramUniv, stream []testdata.Sample, startT, endT int64) {
	t.Logf("--- Correctness Verification (Window: %d - %d) ---", startT, endT)

	// A. Calculate Ground Truth (Exact L2)
	//    Iterate through the *original stream* and filter by timestamp manually.
	exactCounts := make(map[string]int64)
	var totalPacketsInWindow int64

	for _, p := range stream {
		if p.T > endT {
			break
		}
		if p.T >= startT {
			ipKey := strconv.FormatUint(uint64(p.F), 10)
			exactCounts[ipKey]++
			totalPacketsInWindow++
		}
	}

	var exactL2Sq float64
	for _, count := range exactCounts {
		exactL2Sq += float64(count * count)
	}
	exactL2 := math.Sqrt(exactL2Sq)

	t.Logf("Ground Truth: Processed %d packets in window", totalPacketsInWindow)
	t.Logf("Ground Truth L2: %.4f", exactL2)

	// B. Query Sketch
	res, err := eh.QueryInterval(startT, endT)
	if err != nil {
		t.Fatalf("QueryInterval failed: %v", err)
	}

	// Extract L2 from result (HybridSketch or UnivSketch)
	var estimatedL2 float64
	if h, ok := res.(*HybridSketch); ok {
		estimatedL2 = h.GetL2()
	} else if provider, ok := res.(L2Provider); ok {
		estimatedL2 = provider.GetL2()
	} else {
		t.Fatalf("Result sketch does not support L2 retrieval")
	}

	t.Logf("Sketch Estimate L2: %.4f", estimatedL2)

	// C. Error Analysis
	//    Relative Error = |Est - Exact| / Exact
	absErr := math.Abs(estimatedL2 - exactL2)
	relErr := absErr / exactL2

	t.Logf("Absolute Error: %.4f", absErr)
	t.Logf("Relative Error: %.2f%%", relErr*100)

	// D. Theoretical Bound Assertion
	//    UnivMon/CountSketch Error is typically bounded by epsilon * L2_residual.
	//    With Col=200, epsilon is roughly 1/sqrt(200) ~ 7-10% depending on constants.
	//    We set a lenient pass/fail threshold of 15% for this end-to-end test.
	const ErrorThreshold = 0.15 // 15%

	if relErr > ErrorThreshold {
		t.Errorf("FAIL: Relative Error (%.2f%%) exceeds theoretical bound (%.0f%%)",
			relErr*100, ErrorThreshold*100)
	} else {
		t.Logf("PASS: Error is within theoretical bounds.")
	}
}

func TestEH_L1_Accuracy(t *testing.T) {
	// 1. Load Data (Shared)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	file2 := "../../testdata/caida/equinix-nyc.dirA.20181220-130300.UTC.anon.pcap.gz"

	t.Log("Loading CAIDA stream for L1 Accuracy Tests...")
	stream, err := testdata.ReadCAIDAStream(file1, file2)
	if err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	// Use a smaller window for fast verification, but large enough for meaningful merge logic
	windowSize := int64(50000)
	streamSubset := stream[:windowSize] // Use first 50k packets

	// 2. Generate Ground Truth
	t.Log("Calculating Ground Truth...")
	gt := calculateGroundTruth(streamSubset)
	t.Logf("Ground Truth: Total=%d, Distinct=%d, MaxFreq=%d, MedianVal=%.0f",
		gt.TotalCount, gt.DistinctCount, gt.MaxFreq, gt.MedianVal)

	// 3. Define Shared EH Parameters
	k := 50 // EH Precision (High enough to trigger merges)

	// --- TEST CASE A: FREQUENCY SKETCHES (CountMin, CountSketch, Coco) ---
	t.Run("CountMinSketch", func(t *testing.T) {
		eh := NewExpoHistogramCountMin(k, windowSize, 5, 2048)
		feedStringItems(eh, streamSubset)

		// Query top heavy hitter
		est := queryFrequency(t, eh, gt.MaxFreqKey, 0, windowSize)
		checkError(t, "CountMin Freq", float64(gt.MaxFreq), est, 0.05) // 5% error tolerance
	})

	t.Run("CountSketch", func(t *testing.T) {
		eh := NewExpoHistogramCountSketch(k, windowSize, 5, 2048)
		feedStringItems(eh, streamSubset)

		est := queryFrequency(t, eh, gt.MaxFreqKey, 0, windowSize)
		checkError(t, "CountSketch Freq", float64(gt.MaxFreq), est, 0.05)
	})

	t.Run("CocoSketch", func(t *testing.T) {
		eh := NewExpoHistogramCoco(k, windowSize, 3, 2048) // d=3, len=2048
		feedStringItems(eh, streamSubset)

		est := queryFrequency(t, eh, gt.MaxFreqKey, 0, windowSize)
		checkError(t, "CocoSketch Freq", float64(gt.MaxFreq), est, 0.05)
	})

	// --- TEST CASE B: CARDINALITY (HLL) ---
	t.Run("HLL", func(t *testing.T) {
		eh := NewExpoHistogramHLL(k, windowSize)
		feedStringItems(eh, streamSubset)

		// Query Cardinality
		res, err := eh.QueryInterval(0, windowSize)
		if err != nil {
			t.Fatal(err)
		}

		est, err := res.QueryWithHash(common.QueryCardinality, 0)
		if err != nil {
			t.Fatal(err)
		}

		checkError(t, "HLL Cardinality", float64(gt.DistinctCount), est, 0.10) // 10% error (standard HLL is usually <2%)
	})

	// --- TEST CASE C: QUANTILES (KLL, DDS) ---
	t.Run("KLL", func(t *testing.T) {
		eh := NewExpoHistogramKLL(k, windowSize, 200) // K=200
		feedFloatValues(eh, streamSubset)

		// Query Median (p50)
		est := queryQuantile(t, eh, 0.5, 0, windowSize)
		// Quantile values can vary largely, we check rank consistency or approximate value range
		// For simplicity, checking relative value error on the IP integer (treated as float)
		checkError(t, "KLL Median", gt.MedianVal, est, 0.15)
	})

	t.Run("DDSketch", func(t *testing.T) {
		eh := NewExpoHistogramDDS(k, windowSize, 0.02) // alpha=0.02
		feedFloatValues(eh, streamSubset)

		est := queryQuantile(t, eh, 0.5, 0, windowSize)
		checkError(t, "DDSketch Median", gt.MedianVal, est, 0.15)
	})
}

// ---------------- Helpers ----------------

type GroundTruth struct {
	TotalCount    int64
	DistinctCount int64
	MaxFreq       int64
	MaxFreqKey    string
	MedianVal     float64
}

func calculateGroundTruth(stream []testdata.Sample) GroundTruth {
	freqs := make(map[string]int64)
	var values []float64

	for _, p := range stream {
		key := strconv.FormatUint(uint64(p.F), 10)
		freqs[key]++
		values = append(values, p.F)
	}

	var maxF int64
	var maxK string
	for k, v := range freqs {
		if v > maxF {
			maxF = v
			maxK = k
		}
	}

	sort.Float64s(values)
	median := values[len(values)/2]

	return GroundTruth{
		TotalCount:    int64(len(stream)),
		DistinctCount: int64(len(freqs)),
		MaxFreq:       maxF,
		MaxFreqKey:    maxK,
		MedianVal:     median,
	}
}

// Wrapper interface to handle different EH types generically for string insertion
type StringInserter interface {
	UpdateItem(key string, t int64) error
	QueryInterval(t1, t2 int64) (common.Sketch, error)
}

// Wrapper interface for float insertion
type FloatInserter interface {
	UpdateValue(v float64, t int64) error
	QueryInterval(t1, t2 int64) (common.Sketch, error)
}

func feedStringItems(eh StringInserter, stream []testdata.Sample) {
	for _, p := range stream {
		key := strconv.FormatUint(uint64(p.F), 10)
		_ = eh.UpdateItem(key, p.T)
	}
}

func feedFloatValues(eh FloatInserter, stream []testdata.Sample) {
	for _, p := range stream {
		_ = eh.UpdateValue(p.F, p.T)
	}
}

func queryFrequency(t *testing.T, eh StringInserter, key string, t1, t2 int64) float64 {
	res, err := eh.QueryInterval(t1, t2)
	if err != nil {
		t.Fatal(err)
	}

	h := common.FromString(key).Hash
	val, err := res.QueryWithHash(common.QueryFrequency, h)
	if err != nil {
		t.Fatal(err)
	}
	return val
}

func queryQuantile(t *testing.T, eh FloatInserter, q float64, t1, t2 int64) float64 {
	res, err := eh.QueryInterval(t1, t2)
	if err != nil {
		t.Fatal(err)
	}

	// In common.Sketch, QueryQuantile expects the hash to represent the float64 bits of q
	// OR specific sketches expose methods. The generic way via QueryWithHash:
	qBits := math.Float64bits(q)
	val, err := res.QueryWithHash(common.QueryQuantile, qBits)
	if err != nil {
		// Fallback: If generic query fails, try to cast (e.g. for KLL/DDS specific methods)
		// But your provided sketch.go/DDSketch.go suggests QueryWithHash logic is implemented.
		t.Fatalf("QueryQuantile failed: %v", err)
	}
	return val
}

func checkError(t *testing.T, name string, exact, est, tolerance float64) {
	absErr := math.Abs(exact - est)
	relErr := 0.0
	if exact != 0 {
		relErr = absErr / exact
	}

	t.Logf("[%s] Exact: %.2f, Est: %.2f, RelErr: %.2f%%", name, exact, est, relErr*100)

	if relErr > tolerance {
		t.Errorf("FAIL [%s]: Error %.2f%% exceeds tolerance %.2f%%", name, relErr*100, tolerance*100)
	}
}
