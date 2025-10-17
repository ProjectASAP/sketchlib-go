package countminsketch

import (
	"fmt"
	"math"
	"testing"
)

// Helper to build a CMS with deterministic seeds.
func newTestCMS(t *testing.T, rows, cols int) CountMinSketch {
	// Use the first "rows" seeds from SEEDLIST (cast to uint32).
	seed := make([]uint32, rows)
	for i := 0; i < rows; i++ {
		seed[i] = uint32(SEEDLIST[i%len(SEEDLIST)])
	}
	s, err := NewCountMinSketch(rows, cols, seed)
	if err != nil {
		t.Fatalf("NewCountMinSketch error: %v", err)
	}
	return s
}

func almostEq(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// Test core aggregations: Count, Sum, Sum2, L1/L2 norms.
func TestCMS_Aggregations(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	type sample struct {
		key   string
		vals  []float64
		count int
		sum   float64
		sum2  float64
	}
	data := []sample{
		{"alpha", []float64{10, 20, 30, 40, 50}, 5, 150, 10*10 + 20*20 + 30*30 + 40*40 + 50*50},
		{"beta", []float64{3, 3, 3}, 3, 9, 3*3 + 3*3 + 3*3},
		{"gamma", []float64{100}, 1, 100, 100 * 100},
	}

	totalInserts := 0
	for _, smp := range data {
		for _, v := range smp.vals {
			s.CMProcessing(smp.key, v)
			totalInserts++
		}
	}
	t.Logf("[ingest] total inserts = %d (rows=%d, cols=%d)", totalInserts, s.Row(), s.Col())

	// Validation and per-key logging.
	for _, smp := range data {
		estCount := s.EstimateStringCount(smp.key)
		estSum := s.EstimateStringSum(smp.key)
		estSum2 := s.EstimateStringSum2(smp.key)

		t.Logf("[key=%s] count_est=%.0f sum_est=%.2f sum2_est=%.2f", smp.key, estCount, estSum, estSum2)

		if estCount+1e-9 < float64(smp.count) {
			t.Fatalf("count underestimation for key=%s: got %.2f < true %d", smp.key, estCount, smp.count)
		}
		if estCount > float64(smp.count)+2 {
			t.Fatalf("count suspiciously high for key=%s: got %.2f, true %d", smp.key, estCount, smp.count)
		}

		if math.Abs(estSum)+1e-9 < math.Abs(float64(smp.sum)) {
			t.Fatalf("sum underestimation for key=%s: got %.2f < true %.2f", smp.key, estSum, smp.sum)
		}
		if math.Abs(estSum) > math.Abs(float64(smp.sum))+50 {
			t.Fatalf("sum suspiciously high for key=%s: got %.2f vs true %.2f", smp.key, estSum, smp.sum)
		}

		if estSum2+1e-9 < float64(smp.sum2) {
			t.Fatalf("sum2 underestimation for key=%s: got %.2f < true %.2f", smp.key, estSum2, smp.sum2)
		}
		if estSum2 > float64(smp.sum2)+1e4 {
			t.Fatalf("sum2 suspiciously high for key=%s: got %.2f vs true %.2f", smp.key, estSum2, smp.sum2)
		}
	}

	l1 := s.cm_l1()
	if !almostEq(l1, float64(totalInserts), 1e-9) {
		t.Fatalf("cm_l1 expected %.0f, got %.2f", float64(totalInserts), l1)
	}

	l2 := s.cm_l2()
	lower := math.Sqrt(float64(totalInserts))
	upper := float64(totalInserts)
	if l2+1e-9 < lower || l2-1e-9 > upper {
		t.Fatalf("cm_l2 out of bounds: got %.4f, expected within [%.4f, %.4f]", l2, lower, upper)
	}

	t.Logf("[norms] L1=%.0f L2=%.4f", l1, l2)
}

// Empty sketch & unknown keys should return zeros.
func TestCMS_EmptyAndUnknownKeys(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	keys := []string{"alpha", "beta", "gamma"}
	for _, k := range keys {
		if got := s.EstimateStringCount(k); got != 0 {
			t.Fatalf("empty count should be 0 for key=%s, got=%.2f", k, got)
		}
		if got := s.EstimateStringSum(k); got != 0 {
			t.Fatalf("empty sum should be 0 for key=%s, got=%.2f", k, got)
		}
		if got := s.EstimateStringSum2(k); got != 0 {
			t.Fatalf("empty sum2 should be 0 for key=%s, got=%.2f", k, got)
		}
	}

	// After inserting another key, unknown key should remain zero.
	s.CMProcessing("known", 42)
	if got := s.EstimateStringCount("unknown"); got < 0 {
		t.Fatalf("unknown key count must be >= 0, got=%.2f", got)
	}
}

// With fixed seeds, results should be deterministic and reproducible.
func TestCMS_DeterminismWithFixedSeeds(t *testing.T) {
	seed := make([]uint32, CM_ROW_NO)
	for i := 0; i < CM_ROW_NO; i++ {
		seed[i] = uint32(SEEDLIST[i%len(SEEDLIST)])
	}
	s1, err := NewCountMinSketch(CM_ROW_NO, CM_COL_NO, seed)
	if err != nil {
		t.Fatalf("NewCountMinSketch err: %v", err)
	}
	s2, err := NewCountMinSketch(CM_ROW_NO, CM_COL_NO, seed)
	if err != nil {
		t.Fatalf("NewCountMinSketch err: %v", err)
	}

	feed := []struct {
		key string
		val float64
	}{
		{"a", 1}, {"a", 2},
		{"b", 10},
		{"c", -3}, {"c", 7},
	}
	for _, x := range feed {
		s1.CMProcessing(x.key, x.val)
		s2.CMProcessing(x.key, x.val)
	}

	for _, k := range []string{"a", "b", "c", "z"} {
		if s1.EstimateStringCount(k) != s2.EstimateStringCount(k) {
			t.Fatalf("determinism fail (count) key=%s", k)
		}
		if s1.EstimateStringSum(k) != s2.EstimateStringSum(k) {
			t.Fatalf("determinism fail (sum) key=%s", k)
		}
		if s1.EstimateStringSum2(k) != s2.EstimateStringSum2(k) {
			t.Fatalf("determinism fail (sum2) key=%s", k)
		}
	}
}

// Monotonicity: Count should never decrease, while sum/sum2 may fluctuate due to negative values.
// Only enforce monotonicity for count and |sum| on positive inserts.
func TestCMS_Monotonicity(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)
	key := "mono"

	prevCount := s.EstimateStringCount(key)
	prevSum := math.Abs(s.EstimateStringSum(key))
	prevSum2 := s.EstimateStringSum2(key)

	// Mixed sequence of positive and negative inserts.
	steps := []float64{1, 1, -2, 5, -1, 3}
	for i, v := range steps {
		s.CMProcessing(key, v)

		curCount := s.EstimateStringCount(key)
		curSum := math.Abs(s.EstimateStringSum(key))
		curSum2 := s.EstimateStringSum2(key)

		// Count must not decrease.
		if curCount+1e-9 < prevCount {
			t.Fatalf("count decreased at step %d: prev=%.2f cur=%.2f", i, prevCount, curCount)
		}

		// If the inserted value is positive, |sum| should not decrease.
		if v >= 0 && curSum+1e-9 < prevSum {
			t.Fatalf("|sum| decreased after positive insert at step %d: prev=%.2f cur=%.2f", i, prevSum, curSum)
		}

		// sum2 must not decrease (since v² is always positive).
		if curSum2+1e-9 < prevSum2 {
			t.Fatalf("sum2 decreased at step %d: prev=%.2f cur=%.2f", i, prevSum2, curSum2)
		}

		prevCount, prevSum, prevSum2 = curCount, curSum, curSum2
	}
}

// L1/L2 accounting under many inserts (fixed seed → reproducible results).
func TestCMS_L1L2_AccountingMany(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	N := 500
	for i := 0; i < N; i++ {
		k := fmt.Sprintf("k_%03d", i%37) // 37 unique keys → natural collisions
		v := float64((i % 11) - 5)       // values in range [-5..5]
		s.CMProcessing(k, v)
	}

	// cm_l1 = total inserts per row → minimum across rows equals N.
	l1 := s.cm_l1()
	if math.Abs(l1-float64(N)) > 1e-9 {
		t.Fatalf("cm_l1 expected %d, got %.2f", N, l1)
	}

	// cm_l2 bounds: sqrt(N) <= l2 <= N.
	l2 := s.cm_l2()
	if l2+1e-9 < math.Sqrt(float64(N)) || l2-1e-9 > float64(N) {
		t.Fatalf("cm_l2 out of bounds: got=%.4f, expected in [%.4f, %.4f]",
			l2, math.Sqrt(float64(N)), float64(N))
	}
}

// No significant underestimation on tracked keys in high-cardinality workload.
// Small absolute (±5) or relative (±1%) tolerance is allowed for collision noise.
func TestCMS_NoUnderestimateOnTrackedKeys(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	type acc struct{ cnt, sum, sum2 float64 }
	truth := map[string]*acc{
		"hotA": {0, 0, 0},
		"hotB": {0, 0, 0},
		"hotC": {0, 0, 0},
	}

	total := 2000
	for i := 0; i < total; i++ {
		var k string
		switch {
		case i%7 == 0:
			k = "hotA"
		case i%11 == 0:
			k = "hotB"
		case i%13 == 0:
			k = "hotC"
		default:
			k = fmt.Sprintf("cold_%d", i) // many cold (unique) keys
		}

		v := float64((i % 7) - 3) // values in range [-3..3]
		s.CMProcessing(k, v)

		if a, ok := truth[k]; ok {
			a.cnt++
			a.sum += v
			a.sum2 += v * v
		}
	}

	const absTol = 5.0
	const relTol = 0.01

	for k, a := range truth {
		estC := s.EstimateStringCount(k)
		estS := s.EstimateStringSum(k)
		estS2 := s.EstimateStringSum2(k)

		if estC+1e-9 < a.cnt {
			t.Fatalf("under-estimate count %s: est=%.2f < true=%.2f", k, estC, a.cnt)
		}

		// Allow small absolute or relative deviation.
		if math.Abs(estS)+absTol < math.Abs(a.sum)*(1-relTol) {
			t.Fatalf("under-estimate sum %s: |est|=%.2f < |true|=%.2f (tol=±%.1f or ±%.0f%%)",
				k, math.Abs(estS), math.Abs(a.sum), absTol, relTol*100)
		}

		if estS2+absTol < a.sum2*(1-relTol) {
			t.Fatalf("under-estimate sum2 %s: est=%.2f < true=%.2f (tol=±%.1f or ±%.0f%%)",
				k, estS2, a.sum2, absTol, relTol*100)
		}
	}

	t.Logf("NoUnderestimateOnTrackedKeys passed with absTol=%.1f and relTol=%.0f%%", absTol, relTol*100)
}
