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
func TestCS_ZeroState(t *testing.T) {
	cs, _ := NewCountSketch(5, 1024)

	est := float64(cs.EstimateStringCount("never_seen"))
	logResult(t, "ZeroState", 0, est)

	if math.Abs(est) > 1 {
		t.Fatalf("zero-state incorrect")
	}
}

// 2. Single-key exactness
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
