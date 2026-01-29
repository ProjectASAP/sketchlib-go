package countsketch

import (
	"math"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
)

func logResult(t *testing.T, name string, expected, estimated float64) {
	t.Helper()
	err := math.Abs(estimated - expected)
	t.Logf("[%s] expected=%.2f estimated=%.2f abs_error=%.2f",
		name, expected, estimated, err)
}

// 1. Zero-state correctness
// TestCS_ZeroState verifies that a newly initialized CountSketch
// returns zero estimates for keys that have never been observed.
// This ensures there is no initialization bias.
func TestCS_ZeroState(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	est := float64(cs.EstimateStringCount("never_seen"))
	logResult(t, "ZeroState", 0, est)

	if math.Abs(est) > 1 {
		t.Fatalf("zero-state incorrect")
	}
}

// 2. Single-key exactness
// TestCS_SingleKeyCorrectness checks basic correctness by inserting
// a single key multiple times and verifying that the estimated
// frequency matches the true count.
func TestCS_SingleKeyCorrectness(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 1000; i++ {
		cs.UpdateString("key", 1)
	}

	est := float64(cs.EstimateStringCount("key"))
	logResult(t, "SingleKey", 1000, est)

	if math.Abs(est-1000) > 2 {
		t.Fatalf("single-key incorrect")
	}
}

// 3. Linearity
// TestCS_Linearity validates the linearity property of CountSketch:
// multiple independent updates should be equivalent to a single
// combined update of the same total weight.
func TestCS_Linearity(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 300; i++ {
		cs.UpdateString("a", 1)
	}
	for i := 0; i < 700; i++ {
		cs.UpdateString("a", 1)
	}

	est := float64(cs.EstimateStringCount("a"))
	logResult(t, "Linearity", 1000, est)

	if math.Abs(est-1000) > 3 {
		t.Fatalf("linearity violated")
	}
}

// 4. Merge correctness
// TestCS_MergeCorrectness verifies that merging two CountSketch
// instances produces the same result as sketching the union of their input streams.
func TestCS_MergeCorrectness(t *testing.T) {
	cs1, _ := NewCountSketch(5, 1024)
	cs2, _ := NewCountSketch(5, 1024)

	for i := 0; i < 400; i++ {
		cs1.UpdateString("x", 1)
	}
	for i := 0; i < 600; i++ {
		cs2.UpdateString("x", 1)
	}

	if err := cs1.Merge(cs2); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	est := float64(cs1.EstimateStringCount("x"))
	logResult(t, "Merge", 1000, est)

	if math.Abs(est-1000) > 5 {
		t.Fatalf("merge incorrect")
	}
}

// 5. Median estimator correctness
// TestCS_MedianEstimator ensures that the median-of-rows estimator
// is robust to outliers caused by hash collisions in individual rows.
func TestCS_MedianEstimator(t *testing.T) {
	cs, _ := NewCountSketch(3, 1024)
	hash := common.Hash64([]byte("k"))

	// poison one row
	c, sign := cs.derivePosAndSign(hash, 0)
	cs.Count[0][c] += 10_000 * sign

	for i := 0; i < 3; i++ {
		cs.InsertWithHash(hash)
	}

	est, _ := cs.QueryWithHash(common.QueryFrequency, hash)
	logResult(t, "MedianEstimator", 3, est)

	if est < 2 || est > 4 {
		t.Fatalf("median estimator broken")
	}
}

// 6. Sign correctness
// TestCS_SignCorrectness verifies that the sign hashing mechanism
// correctly handles positive and negative updates, allowing
// proper cancellation of counts.
func TestCS_SignCorrectness(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 100; i++ {
		cs.UpdateString("pos", 1)
		cs.UpdateString("neg", -1)
	}

	estPos := float64(cs.EstimateStringCount("pos"))
	estNeg := float64(cs.EstimateStringCount("neg"))

	t.Logf("[Sign] pos_est=%.2f neg_est=%.2f", estPos, estNeg)

	if estPos <= 0 || estNeg >= 0 {
		t.Fatalf("sign incorrect")
	}
}

// 7. Query purity
// TestCS_QueryNoSideEffect ensures that calling query operations
// does not mutate the internal state of the sketch.
// Queries must be pure read-only operations.
func TestCS_QueryNoSideEffect(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	cs.UpdateString("x", 1)
	before := float64(cs.EstimateStringCount("x"))

	for i := 0; i < 100; i++ {
		cs.EstimateStringCount("x")
	}

	after := float64(cs.EstimateStringCount("x"))
	logResult(t, "QueryPurity", before, after)

	if before != after {
		t.Fatalf("query has side effects")
	}
}

// 8. Order independence (commutativity)
// TestCS_OrderIndependence checks that the order of updates
// does not affect the final frequency estimates.
// CountSketch should be commutative with respect to updates.
func TestCS_OrderIndependence(t *testing.T) {
	cs1, _ := NewCountSketch(5, 1024)
	cs2, _ := NewCountSketch(5, 1024)

	// A then B
	cs1.UpdateString("a", 1)
	cs1.UpdateString("b", 1)

	// B then A
	cs2.UpdateString("b", 1)
	cs2.UpdateString("a", 1)

	est1a := float64(cs1.EstimateStringCount("a"))
	est2a := float64(cs2.EstimateStringCount("a"))
	est1b := float64(cs1.EstimateStringCount("b"))
	est2b := float64(cs2.EstimateStringCount("b"))

	t.Logf("[OrderIndependence] a:(%.2f, %.2f) b:(%.2f, %.2f)",
		est1a, est2a, est1b, est2b)

	if math.Abs(est1a-est2a) > 1 || math.Abs(est1b-est2b) > 1 {
		t.Fatalf("order independence violated")
	}
}

// 9. Multiple keys isolation
// TestCS_KeyIsolation verifies that updates to one key do not
// significantly affect the estimates of other unrelated keys.
// This tests isolation under hash collisions.
func TestCS_KeyIsolation(t *testing.T) {
	cs, _ := NewCountSketch(5, 2048)

	for i := 0; i < 1000; i++ {
		cs.UpdateString("hot", 1)
		cs.UpdateString("cold", 1)
	}

	estHot := float64(cs.EstimateStringCount("hot"))
	estCold := float64(cs.EstimateStringCount("cold"))

	t.Logf("[Isolation] hot=%.2f cold=%.2f", estHot, estCold)

	if math.Abs(estHot-1000) > 5 || math.Abs(estCold-1000) > 5 {
		t.Fatalf("key isolation broken")
	}
}

// 10. Weighted update correctness
// TestCS_WeightedUpdates verifies that CountSketch correctly
// supports weighted updates (delta != 1), which are common
// in aggregated or pre-processed streams.
func TestCS_WeightedUpdates(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	cs.UpdateString("w", 2)
	cs.UpdateString("w", 3)
	cs.UpdateString("w", 5)

	est := float64(cs.EstimateStringCount("w"))
	logResult(t, "WeightedUpdate", 10, est)

	if math.Abs(est-10) > 2 {
		t.Fatalf("weighted update incorrect")
	}
}

// 11. Positive-negative cancellation
// TestCS_Cancellation verifies algebraic cancellation: a sequence
// of positive and negative updates with equal magnitude should
// result in a zero estimate.
func TestCS_Cancellation(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 100; i++ {
		cs.UpdateString("x", 1)
		cs.UpdateString("x", -1)
	}

	est := float64(cs.EstimateStringCount("x"))
	logResult(t, "Cancellation", 0, est)

	if math.Abs(est) > 1 {
		t.Fatalf("cancellation failed")
	}
}

// 12. Idempotent merge with empty sketch
// TestCS_MergeWithEmpty ensures that merging a sketch with an
// empty sketch does not change its internal state.
// This validates idempotence and the existence of a neutral element.
func TestCS_MergeWithEmpty(t *testing.T) {
	cs1, _ := NewCountSketch(5, 1024)
	cs2, _ := NewCountSketch(5, 1024) // empty

	cs1.UpdateString("z", 100)

	before := float64(cs1.EstimateStringCount("z"))
	if err := cs1.Merge(cs2); err != nil {
		t.Fatalf("merge failed")
	}
	after := float64(cs1.EstimateStringCount("z"))

	logResult(t, "MergeEmpty", before, after)

	if before != after {
		t.Fatalf("merge with empty changed state")
	}
}
