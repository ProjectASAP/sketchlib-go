package exponentialhistogram

import (
	"math/rand"
	"testing"
	"time"
)

// ==============================================================================
// TEST SUITE: EH STRUCTURE & LOGIC CORRECTNESS
// Uses 'ExpoHistogramCount' (Exact Counter) to verify logic.
// ==============================================================================

// 1. Test Sliding Window Logic
// Ensures old data is removed, while accounting for the approximation nature of EH.
func TestLogic_SlidingWindow(t *testing.T) {
	k := 10
	windowSize := int64(100) // Window 100ms
	eh := NewExpoHistogramCount(k, windowSize)

	t.Log("Testing Sliding Window Logic...")

	// Phase 1: Fill the window completely (t=0 to t=99)
	for i := int64(0); i < 100; i++ {
		eh.UpdateCount(1, i)
	}

	// Check Total: Should be 100 (since no sliding yet, buckets are usually precision 1)
	total, _ := eh.GetTotalCount(0, 100)
	if total != 100 {
		t.Errorf("Phase 1 Failed: Expected 100, got %d", total)
	}

	// Phase 2: Slide the window (t=100 to t=149)
	// Items t=0..49 should be expired.
	// Items t=50..149 are valid (total 100 items).
	for i := int64(100); i < 150; i++ {
		eh.UpdateCount(1, i)
	}

	// Query wide range covering all active buckets
	totalAfterSlide, _ := eh.GetTotalCount(0, 150)

	// CORRECTNESS ANALYSIS FOR EH:
	// EH is an approximate data structure.
	// The oldest bucket might overlap with the window boundary.
	// EH "Upper Bound" will keep the overlapping bucket, so result > 100.
	// Max EH error bound is 1/k.
	// With k=10, error ~10%. So 100 <= result <= 110 is valid.

	expected := int64(100)
	// Relative error tolerance (due to bucket boundary)
	// We allow slight over-estimation due to the granular nature of buckets.
	maxAllowed := expected + int64(float64(expected)/float64(k)) + 2 // 100 + 10 + buffer

	if totalAfterSlide < expected || totalAfterSlide > maxAllowed {
		t.Errorf("Phase 2 Failed (Sliding): Expected approx %d, got %d. (Allowed range: %d-%d)",
			expected, totalAfterSlide, expected, maxAllowed)

		// Debug: Check bucket structure to see why it exceeded
		t.Logf("Debug Buckets State:")
		threshold := int64(150) - windowSize
		for i, b := range eh.buckets {
			status := "VALID"
			if b.MaxTime < threshold {
				status = "EXPIRED (SHOULD BE DELETED)"
			}
			t.Logf("[%d] Range[%d-%d] Count=%d (%s)", i, b.MinTime, b.MaxTime, b.Count, status)
		}
	} else {
		t.Logf("Phase 2 Success: Got %d (Expected %d with approximation tolerance)", totalAfterSlide, expected)
	}

	// Internal Verification: Ensure truly expired buckets are removed
	// A bucket is considered totally expired if MaxTime < threshold.
	if len(eh.buckets) > 0 {
		oldestBucket := eh.buckets[0]
		threshold := int64(150) - windowSize // 50

		// If MaxTime < 50, it means it's garbage that wasn't collected
		if oldestBucket.MaxTime < threshold {
			t.Errorf("Violation: Oldest bucket MaxTime (%d) is strictly older than threshold (%d). It should have been removed.", oldestBucket.MaxTime, threshold)
		}
	}
}

// 2. Test EH Invariants (Exponential Growth & Order)
// Ensures Datar et al. rules are met:
// - Buckets sorted by time
// - Bucket sizes grow exponentially (1, 1, 2, 2, 4, 4...)
// - Max buckets per size <= k/2 + 2
func TestLogic_Invariants(t *testing.T) {
	k := 2 // Very small K forces frequent merges
	windowSize := int64(10000)
	eh := NewExpoHistogramCount(k, windowSize)

	// Insert 32 items (1 per ms).
	// With k=2, we expect buckets to distribute into powers of 2.
	for i := int64(0); i < 32; i++ {
		eh.UpdateCount(1, i)
	}

	t.Log("Inspecting Bucket Structure (k=2, N=32)...")

	// Validation 1: Time Ordering (Monotonic)
	for i := 0; i < len(eh.buckets)-1; i++ {
		curr := eh.buckets[i]
		next := eh.buckets[i+1]

		if curr.MinTime >= next.MinTime {
			t.Errorf("Invariant Violation: Buckets not sorted by time. B[%d].MinTime (%d) >= B[%d].MinTime (%d)", i, curr.MinTime, i+1, next.MinTime)
		}
	}

	// Validation 2: Bucket Size constraints
	// Count bucket size histogram
	sizeCounts := make(map[int64]int)
	for _, b := range eh.buckets {
		sizeCounts[b.Count]++

		// Validation: Size must be Power of 2 (1, 2, 4, 8...)
		if !isPowerOfTwo(b.Count) {
			t.Errorf("Invariant Violation: Bucket size %d is not power of 2", b.Count)
		}
	}

	// Validation 3: Max buckets per level
	// EH Rule: max k/2 + 2 buckets of the same size
	limit := k/2 + 2
	for size, count := range sizeCounts {
		if count > limit {
			t.Errorf("Invariant Violation: Too many buckets of size %d. Have %d, Max allowed %d", size, count, limit)
		}
		t.Logf("Size %d: %d buckets", size, count)
	}
}

// 3. Test Merge Correctness
// Ensures 1 + 1 = 2, and no data loss during merge.
func TestLogic_MergeCorrectness(t *testing.T) {
	k := 10
	windowSize := int64(100000) // Infinite window
	eh := NewExpoHistogramCount(k, windowSize)

	n := 1000
	// Insert 1000 items
	for i := int64(0); i < int64(n); i++ {
		eh.UpdateCount(1, i)
	}

	// Total must be EXACTLY 1000
	// If merge is wrong, this number will drift (e.g., 999 or 1001)
	total, _ := eh.GetTotalCount(0, int64(n))

	if total != int64(n) {
		t.Errorf("Merge Logic Fail: Inserted %d, Counted %d. Data lost or duplicated during merge.", n, total)
	}
}

// 4. Test Query Interval Consistency
// Ensures sub-interval query returns reasonable results
// (Based on EH overlap logic)
func TestLogic_QueryInterval(t *testing.T) {
	k := 20
	window := int64(100)
	eh := NewExpoHistogramCount(k, window)

	// Scenario:
	// T=10: insert 1
	// T=20: insert 1
	// T=30: insert 1
	// T=40: insert 1
	// T=50: insert 1
	times := []int64{10, 20, 30, 40, 50}
	for _, tm := range times {
		eh.UpdateCount(1, tm)
	}

	// Query Interval [15, 45]
	// Should cover buckets T=20, T=30, T=40. Total = 3.
	// Note: EH overlap implementation might cover T=10 or T=50 if buckets are merged.
	// But since N=5 and k=20, no merge yet. Buckets are size=1.
	// So must be exact 3.

	count, err := eh.GetTotalCount(15, 45)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if count != 3 {
		t.Errorf("Interval Query Fail: Range [15, 45] should cover {20,30,40}. Expected 3, got %d", count)
		// Debug print
		for i, b := range eh.buckets {
			t.Logf("Bucket %d: Range[%d-%d] Count=%d", i, b.MinTime, b.MaxTime, b.Count)
		}
	}
}

// 5. Test Determinism
// Ensures 2 EH instances with same input produce identical structure.
// Must not have race conditions or random factors in bucket logic.
func TestLogic_Determinism(t *testing.T) {
	seed := time.Now().UnixNano()
	r := rand.New(rand.NewSource(seed))

	inputData := make([]int64, 1000)
	for i := 0; i < 1000; i++ {
		inputData[i] = r.Int63n(100000) // Random timestamps (monotonic simulation)
	}
	sortInt64(inputData) // Ensure monotonicity

	eh1 := NewExpoHistogramCount(5, 50000)
	eh2 := NewExpoHistogramCount(5, 50000)

	// Feed EH1
	for _, tm := range inputData {
		eh1.UpdateCount(1, tm)
	}
	// Feed EH2
	for _, tm := range inputData {
		eh2.UpdateCount(1, tm)
	}

	// Compare Structure
	if len(eh1.buckets) != len(eh2.buckets) {
		t.Fatalf("Determinism Fail: Bucket count mismatch. EH1=%d, EH2=%d", len(eh1.buckets), len(eh2.buckets))
	}

	for i := 0; i < len(eh1.buckets); i++ {
		b1 := eh1.buckets[i]
		b2 := eh2.buckets[i]

		if b1.Count != b2.Count || b1.MinTime != b2.MinTime || b1.MaxTime != b2.MaxTime {
			t.Errorf("Determinism Fail at Bucket %d:\nEH1: [%d-%d] size %d\nEH2: [%d-%d] size %d",
				i, b1.MinTime, b1.MaxTime, b1.Count, b2.MinTime, b2.MaxTime, b2.Count)
		}
	}
}

// --- Helpers ---

func isPowerOfTwo(x int64) bool {
	return (x != 0) && ((x & (x - 1)) == 0)
}

func sortInt64(a []int64) {
	// Manual bubble sort implementation
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[i] > a[j] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}
