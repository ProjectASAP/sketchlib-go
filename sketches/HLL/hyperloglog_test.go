package hll

import (
	"fmt"
	"math"
	"testing"
)

const (
	relativeTolerance = 0.02
)

func TestHyperLogLogEstimateAccuracy(t *testing.T) {
	for exp := 0; exp < 10; exp++ {
		exp := exp
		t.Run(fmt.Sprintf("1e%d", exp), func(t *testing.T) {
			actual := int(math.Pow10(exp))
			h := NewHyperLogLog()
			for i := 0; i < actual; i++ {
				h.Insert(float64(i))
			}
			estimate := h.Estimate()
			assertWithinRelativeError(t, estimate, actual, relativeTolerance)
		})
	}
}

func TestHyperLogLogIgnoresDuplicates(t *testing.T) {
	h := NewHyperLogLog()
	for i := 0; i < 1000; i++ {
		h.Insert(42.0)
	}

	if estimate := h.Estimate(); estimate != 1 {
		t.Fatalf("expected cardinality 1, got %d", estimate)
	}
}

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
		t.Fatalf("estimate %d differs from actual %d beyond tolerance %.2f", estimate, actual, tolerance)
	}
}
