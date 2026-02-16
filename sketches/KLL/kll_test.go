package kll

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/testdata"
)

// ======================
// Helpers
// ======================

// Helper to load CAIDA data
func loadCAIDA(t *testing.T) []float64 {
	// Adjust path relative to this file location (sketches/kll/)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Skip("No CAIDA samples found.")
	}

	// Extract just the float values (IPs) for KLL processing
	data := make([]float64, len(samples))
	for i, s := range samples {
		data[i] = s.F
	}
	return data
}

func newTestKLL(t *testing.T, k int) *KLLSketch {
	s, err := NewKLLSketch(k)
	if err != nil {
		t.Fatalf("Failed to init KLLSketch(k=%d): %v", k, err)
	}
	return s
}

// ======================
// Basic Correctness (CAIDA)
// ======================

// TestKLL_CAIDA_BasicFlow verifies ingestion of real data.
func TestKLL_CAIDA_BasicFlow(t *testing.T) {
	data := loadCAIDA(t)
	k := 200
	s := newTestKLL(t, k)

	// Ingest
	for _, v := range data {
		s.Insert(v)
	}

	// Verify Size
	if s.GetSize() != len(data) {
		t.Fatalf("Size mismatch. Expected %d, got %d", len(data), s.GetSize())
	}
	if s.Count() != len(data) {
		t.Fatalf("Count mismatch. Expected %d, got %d", len(data), s.Count())
	}

	// Verify a rank query doesn't panic
	_ = s.Rank(data[0])
}

// ======================
// Statistical Accuracy (CAIDA)
// ======================

// TestKLL_CAIDA_Accuracy measures rank estimation error against ground truth.
// KLL guarantees rank error <= 1.0 / k with high probability.
func TestKLL_CAIDA_Accuracy(t *testing.T) {
	data := loadCAIDA(t)

	// Configuration
	k := 200
	// Theoretical Error Bound for KLL is roughly 1/k to 2/k depending on implementation details
	// We allow a small margin.
	maxRankError := 2.0 / float64(k) // ~1.0%

	s := newTestKLL(t, k)
	for _, v := range data {
		s.Insert(v)
	}

	// Ground Truth: Sort the data
	sortedData := make([]float64, len(data))
	copy(sortedData, data)
	sort.Float64s(sortedData)

	// Test Points (Quantiles)
	checkPoints := []float64{0.5, 0.9, 0.99}

	t.Log("===================================================")
	t.Logf(" CAIDA KLL ACCURACY REPORT (k=%d, N=%d)", k, len(data))
	t.Logf(" Max Allowed Rank Error: %.4f%%", maxRankError*100)
	t.Log("===================================================")

	maxObserved := 0.0

	for _, p := range checkPoints {
		// Calculate index for quantile p
		idx := int(float64(len(sortedData)) * p)
		if idx >= len(sortedData) {
			idx = len(sortedData) - 1
		}
		val := sortedData[idx]

		// True Rank (Normalized 0..1)
		trueRank := float64(idx) / float64(len(data))

		// Estimated Rank (Normalized 0..1)
		// KLL Rank() returns approximate number of items <= val
		estCount := s.Rank(val)
		estRank := float64(estCount) / float64(len(data))

		err := math.Abs(estRank - trueRank)
		if err > maxObserved {
			maxObserved = err
		}

		t.Logf(" p=%-4.2f | Val: %10.0f | TrueRank: %.4f | EstRank: %.4f | Err: %.4f%%",
			p, val, trueRank, estRank, err*100)

		if err > maxRankError {
			t.Errorf("Rank error %.4f%% exceeds limit %.4f%% at p=%.2f", err*100, maxRankError*100, p)
		}
	}

	t.Log("---------------------------------------------------")
	t.Logf(" Max Observed Error: %.4f%%", maxObserved*100)
}

// ======================
// Merge Tests (CAIDA)
// ======================

// TestKLL_CAIDA_Merge splits the CAIDA stream, sketches separately,
// merges, and compares against the full sketch.
func TestKLL_CAIDA_Merge(t *testing.T) {
	data := loadCAIDA(t)
	k := 200
	mid := len(data) / 2

	sPart1 := newTestKLL(t, k)
	sPart2 := newTestKLL(t, k)
	sTotal := newTestKLL(t, k)

	for i, v := range data {
		sTotal.Insert(v)
		if i < mid {
			sPart1.Insert(v)
		} else {
			sPart2.Insert(v)
		}
	}

	// Merge
	if err := sPart1.Merge(sPart2); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	// Verify Total Count
	if sPart1.GetSize() != len(data) {
		t.Errorf("Merged size mismatch. Expected %d, got %d", len(data), sPart1.GetSize())
	}

	// Verify Quantile Consistency
	// The merged sketch should have similar quantile estimates to the total sketch
	// Note: KLL merge is approximate, so we check if they are "close enough"
	qMerged := sPart1.Quantile(0.5) // Median
	qTotal := sTotal.Quantile(0.5)

	// Allow slight deviation due to merge approximation
	// We verify they are within 5% of the total value range
	minVal := sTotal.Quantile(0.0)
	maxVal := sTotal.Quantile(1.0)
	rangeVal := maxVal - minVal

	diff := math.Abs(qMerged - qTotal)
	relDiff := diff / rangeVal

	t.Logf("Merge Median Check: Merged=%.0f, Total=%.0f, RelDiff=%.4f", qMerged, qTotal, relDiff)

	if relDiff > 0.05 { // 5% tolerance relative to range
		t.Errorf("Merge resulted in significant deviation. RelDiff: %.4f", relDiff)
	}
}

// ======================
// Distribution Properties (CAIDA)
// ======================

// TestKLL_CAIDA_QuantileMonotonicity ensures that on real data,
// requesting higher quantiles returns >= values.
func TestKLL_CAIDA_QuantileMonotonicity(t *testing.T) {
	data := loadCAIDA(t)
	s := newTestKLL(t, 200)

	for _, v := range data {
		s.Insert(v)
	}

	prevVal := math.Inf(-1)
	steps := 20 // Check every 5%

	for i := 0; i <= steps; i++ {
		p := float64(i) / float64(steps)
		val := s.Quantile(p) // Using CDF-like query

		if val < prevVal {
			t.Fatalf("Monotonicity violation at p=%.2f. Prev=%.0f, Curr=%.0f", p, prevVal, val)
		}
		prevVal = val
	}
	t.Log("Monotonicity verified on CAIDA dataset.")
}

// ======================
// Memory & Space Guarantees (CAIDA)
// ======================

// TestKLL_CAIDA_MemoryBound checks that the sketch stays small
// even when ingesting the full CAIDA dataset.
func TestKLL_CAIDA_MemoryBound(t *testing.T) {
	data := loadCAIDA(t)
	k := 200
	s := newTestKLL(t, k)

	start := time.Now()
	for _, v := range data {
		s.Insert(v)
	}
	duration := time.Since(start)

	retained := s.GetRetainedItems()
	memBytes := s.GetMemoryBytes()

	t.Logf("Processed %d items in %v", len(data), duration)
	t.Logf("Retained Items: %d", retained)
	t.Logf("Estimated Memory: %.2f KB", memBytes/1024)

	// Theoretical limit check: num_items <= k * 3 * log2(N/k) roughly
	// For k=200, it should be small.
	if retained > k*50 { // Loose upper bound safety check
		t.Errorf("Retained items %d seems excessive for k=%d", retained, k)
	}
}
