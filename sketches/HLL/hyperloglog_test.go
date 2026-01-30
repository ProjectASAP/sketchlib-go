package hll

import (
	"fmt"
	"math"
	"testing"

	common "github.com/approx-telemetry/sketchlib-go/common"
)

const (
	relativeTolerance = 0.02
)

//
// -----------------------
// ACCURACY TESTS
// -----------------------
//

// Tests estimation accuracy across different cardinalities.
func TestHyperLogLogEstimateAccuracy(t *testing.T) {
	for exp := 0; exp < 10; exp++ {
		exp := exp
		t.Run(fmt.Sprintf("1e%d", exp), func(t *testing.T) {
			actual := int(math.Pow10(exp))
			h := NewHyperLogLog()

			for i := 0; i < actual; i++ {
				h.Insert(float64(i)) // slow path
			}

			estimate := h.Estimate()
			assertWithinRelativeError(t, estimate, actual, relativeTolerance)
		})
	}
}

// Ensures duplicate values do not affect cardinality.
func TestHyperLogLogIgnoresDuplicates(t *testing.T) {
	h := NewHyperLogLog()

	for i := 0; i < 1000; i++ {
		h.Insert(42.0)
	}

	if estimate := h.Estimate(); estimate != 1 {
		t.Fatalf("expected cardinality 1, got %d", estimate)
	}
}

//
// -----------------------
// FAST PATH TESTS
// -----------------------
//

// Ensures InsertWithHash produces equivalent results to Insert.
func TestHyperLogLogFastPathMatchesSlowPath(t *testing.T) {
	actual := 10000

	hSlow := NewHyperLogLog()
	hFast := NewHyperLogLog()

	for i := 0; i < actual; i++ {
		// slow path
		hSlow.Insert(float64(i))

		// fast path
		buf := common.Float64ToBytes(float64(i))
		hash := common.HashIt(0, buf)
		hFast.InsertWithHash(hash)
	}

	estSlow := hSlow.Estimate()
	estFast := hFast.Estimate()

	assertWithinRelativeError(t, estFast, estSlow, relativeTolerance)
}

// Ensures duplicates are ignored in fast path as well.
func TestHyperLogLogFastPathIgnoresDuplicates(t *testing.T) {
	h := NewHyperLogLog()

	buf := common.Float64ToBytes(42.0)
	hash := common.HashIt(0, buf)

	for i := 0; i < 1000; i++ {
		h.InsertWithHash(hash)
	}

	if estimate := h.Estimate(); estimate != 1 {
		t.Fatalf("expected cardinality 1, got %d", estimate)
	}
}

//
// -----------------------
// MERGE TESTS
// -----------------------
//

// Tests merging two sketches with disjoint sets.
func TestHyperLogLogMerge(t *testing.T) {
	first := NewHyperLogLog()
	second := NewHyperLogLog()

	for i := 0; i < 5000; i++ {
		first.Insert(float64(i))
	}
	for i := 5000; i < 10000; i++ {
		second.Insert(float64(i))
	}

	if err := first.Merge(second); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	assertWithinRelativeError(t, first.Estimate(), 10000, relativeTolerance)
}

// Tests merging using fast path sketches.
func TestHyperLogLogMergeFastPath(t *testing.T) {
	first := NewHyperLogLog()
	second := NewHyperLogLog()

	for i := 0; i < 5000; i++ {
		buf := common.Float64ToBytes(float64(i))
		hash := common.HashIt(0, buf)
		first.InsertWithHash(hash)
	}
	for i := 5000; i < 10000; i++ {
		buf := common.Float64ToBytes(float64(i))
		hash := common.HashIt(0, buf)
		second.InsertWithHash(hash)
	}

	if err := first.Merge(second); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	assertWithinRelativeError(t, first.Estimate(), 10000, relativeTolerance)
}

//
// -----------------------
// HELPERS
// -----------------------
//

func assertWithinRelativeError(t *testing.T, estimate, actual int, tolerance float64) {
	t.Helper()

	if actual == 0 {
		if estimate != 0 {
			t.Fatalf("expected zero estimate, got %d", estimate)
		}
		return
	}

	diff := math.Abs(float64(estimate - actual))
	if diff/float64(actual) > tolerance {
		t.Fatalf(
			"estimate %d differs from actual %d beyond tolerance %.2f",
			estimate,
			actual,
			tolerance,
		)
	}
}
