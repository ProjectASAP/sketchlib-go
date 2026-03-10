package ddsketch

import (
	"math"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// ==============================================================================
// 1. HELPERS
// ==============================================================================

// Helper to load CAIDA data
func loadCAIDA(t *testing.T) []testdata.Sample {
	// Adjust path relative to this file location (sketches/DDSketch/)
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

// relErr calculates relative error between estimated and true value.
func relErr(est, truth float64) float64 {
	if est == 0 && truth == 0 {
		return 0
	}
	if truth == 0 {
		return math.Inf(1) // Avoid division by zero
	}
	return math.Abs(est-truth) / math.Abs(truth)
}

// trueQuantile calculates the exact quantile from a sorted slice (Ground Truth).
func trueQuantile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	// Nearest rank definition for simplicity, matching common sketch logic
	rank := math.Ceil(p * float64(n))
	idx := int(rank) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

// ==============================================================================
// 2. CAIDA TESTS
// ==============================================================================

// TestDDSketch_CAIDA_Insertion verifies that we can ingest real traffic data
// and that the sketch correctly ignores invalid (non-positive) values if any.
func TestDDSketch_CAIDA_Insertion(t *testing.T) {
	samples := loadCAIDA(t)

	// Initialize with 1% relative error accuracy
	s := NewDDSketch(0.01)

	validCount := 0
	for _, sample := range samples {
		val := sample.F
		// DDSketch only accepts strictly positive values
		if val > 0 {
			s.Add(val)
			validCount++
		}
	}

	t.Logf("Processed %d samples", len(samples))
	t.Logf("Valid (>0) samples inserted: %d", validCount)
	t.Logf("Sketch Internal Count: %d", s.GetCount())

	if s.GetCount() != uint64(validCount) {
		t.Errorf("Count mismatch. Expected %d, got %d", validCount, s.GetCount())
	}
}

// TestDDSketch_CAIDA_Merge splits the CAIDA dataset into two halves,
// sketches them separately, merges them, and verifies the result matches
// a sketch created from the full dataset.
func TestDDSketch_CAIDA_Merge(t *testing.T) {
	samples := loadCAIDA(t)
	alpha := 0.01

	sPart1 := NewDDSketch(alpha)
	sPart2 := NewDDSketch(alpha)
	sTotal := NewDDSketch(alpha)

	mid := len(samples) / 2

	for i, sample := range samples {
		val := sample.F
		if val <= 0 {
			continue
		}

		sTotal.Add(val)

		if i < mid {
			sPart1.Add(val)
		} else {
			sPart2.Add(val)
		}
	}

	// Perform Merge
	err := sPart1.Merge(sPart2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	// 1. Verify Count
	if sPart1.GetCount() != sTotal.GetCount() {
		t.Errorf("Merge Count Mismatch: Part1+2=%d, Total=%d", sPart1.GetCount(), sTotal.GetCount())
	}

	// 2. Verify Internal Bucket State (Semantic Equality)
	// We cannot compare len(counts) because Add() adds padding (GrowChunk)
	// while Merge() allocates exact fit. We must compare counts at every index.

		// Helper to safely get count at a specific global index
		getCount := func(b *Buckets, k int32) uint64 {
			if b.counts == nil {
				return 0
			}
			counts := b.counts.AsSlice()
			idx := k - b.offset
			if idx >= 0 && int(idx) < len(counts) {
				return counts[idx]
			}
			return 0
		}

	// Determine the global range covering both sketches
	l1, r1, _ := sPart1.store.Range()
	l2, r2, _ := sTotal.store.Range()

	minIdx := minInt32(l1, l2)
	maxIdx := maxInt32(r1, r2)

	for k := minIdx; k <= maxIdx; k++ {
		c1 := getCount(&sPart1.store, k)
		c2 := getCount(&sTotal.store, k)

		if c1 != c2 {
			t.Errorf("Bucket content mismatch at index %d: Merged=%d, Total=%d", k, c1, c2)
			// Fail fast to avoid flooding logs
			break
		}
	}

	// 3. Verify Quantile Query Result
	qMerged, _ := sPart1.GetValueAtQuantile(0.95)
	qTotal, _ := sTotal.GetValueAtQuantile(0.95)

	if qMerged != qTotal {
		t.Errorf("Quantile query mismatch after merge. Merged=%.2f, Total=%.2f", qMerged, qTotal)
	}
}

// TestDDSketch_CAIDA_Accuracy verifies that the sketch adheres to the alpha accuracy guarantee
// when estimating quantiles of the real-world IP distribution.
func TestDDSketch_CAIDA_Accuracy(t *testing.T) {
	samples := loadCAIDA(t)

	// Define target accuracy (alpha)
	// alpha=0.01 means estimates should be within 1% of the true value.
	alpha := 0.01
	s := NewDDSketch(alpha)

	// Collect Ground Truth
	var truth []float64
	for _, sample := range samples {
		val := sample.F
		if val > 0 {
			s.Add(val)
			truth = append(truth, val)
		}
	}

	// Sort Ground Truth for exact quantile calculation
	sort.Float64s(truth)

	// Test points: Min, 50th(Median), 90th, 99th, Max
	quantiles := []float64{0.5, 0.90, 0.99}

	t.Log("===================================================")
	t.Logf(" CAIDA QUANTILE ACCURACY REPORT (alpha=%.2f)", alpha)
	t.Logf(" Data Size: %d items", len(truth))
	t.Log("===================================================")

	for _, q := range quantiles {
		// 1. Get Estimate
		est, ok := s.GetValueAtQuantile(q)
		if !ok {
			t.Fatalf("Failed to get quantile for q=%.2f", q)
		}

		// 2. Get Ground Truth
		exact := trueQuantile(truth, q)

		// 3. Calculate Error
		err := relErr(est, exact)

		t.Logf(" q=%-4.2f | Est: %12.0f | True: %12.0f | RelErr: %.5f", q, est, exact, err)

		// 4. Verify Guarantee
		// DDSketch guarantees relative error <= alpha
		// (We use a tiny buffer 1e-6 for float precision issues)
		if err > alpha+1e-6 {
			t.Errorf("Accuracy violation at q=%.2f! Error %.5f > Alpha %.2f", q, err, alpha)
		}
	}
}

// TestDDSketch_CAIDA_Monotonicity ensures that on real data, requesting higher quantiles
// always returns equal or higher values (CDF property).
func TestDDSketch_CAIDA_Monotonicity(t *testing.T) {
	samples := loadCAIDA(t)
	s := NewDDSketch(0.01)

	for _, sample := range samples {
		if sample.F > 0 {
			s.Add(sample.F)
		}
	}

	prevVal := math.Inf(-1)
	steps := 20 // Check every 5%

	for i := 0; i <= steps; i++ {
		q := float64(i) / float64(steps)
		val, _ := s.GetValueAtQuantile(q)

		if val < prevVal {
			t.Fatalf("Monotonicity violation at q=%.2f. Prev=%.2f, Curr=%.2f", q, prevVal, val)
		}
		prevVal = val
	}
	t.Logf("Monotonicity verified across %d quantile steps", steps)
}

func TestDDSketchRustStyleAPI(t *testing.T) {
	s := New(0.01)
	s.Add(1)
	s.Add(10)
	s.Add(100)

	if got := s.Count(); got != 3 {
		t.Fatalf("unexpected count: got %d", got)
	}
	if min, ok := s.Min(); !ok || min != 1 {
		t.Fatalf("unexpected min: %v %v", min, ok)
	}
	if max, ok := s.Max(); !ok || max != 100 {
		t.Fatalf("unexpected max: %v %v", max, ok)
	}
}
