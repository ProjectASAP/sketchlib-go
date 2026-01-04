package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// =====Helpers=====
func newTestCMS(t *testing.T, rows, cols int) *CountMinSketch {
	s, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountMinSketch error: %v", err)
	}
	return s
}

func almostEq(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// =====Tests=====
func TestCMS_Aggregations(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	type sample struct {
		key string
		n   int
	}

	data := []sample{
		{"alpha", 5},
		{"beta", 3},
		{"gamma", 1},
	}

	total := 0
	for _, smp := range data {
		in := common.FromString(smp.key)
		for i := 0; i < smp.n; i++ {
			s.InsertWithHash(in.Hash)
			total++
		}
	}

	for _, smp := range data {
		in := common.FromString(smp.key)

		c, _ := s.QueryWithHash(common.QueryFrequency, in.Hash)
		sum, _ := s.QueryWithHash(common.QuerySum, in.Hash)
		sum2, _ := s.QueryWithHash(common.QuerySum2, in.Hash)

		if c < float64(smp.n) {
			t.Fatalf("count underestimation key=%s", smp.key)
		}
		if sum < float64(smp.n) {
			t.Fatalf("sum underestimation key=%s", smp.key)
		}
		if sum2 < float64(smp.n) {
			t.Fatalf("sum2 underestimation key=%s", smp.key)
		}
	}

	l1 := s.CM_L1()
	if !almostEq(l1, float64(total), 1e-9) {
		t.Fatalf("L1 expected %.0f, got %.2f", float64(total), l1)
	}

	l2 := s.CM_L2()
	if l2 < math.Sqrt(float64(total)) || l2 > float64(total) {
		t.Fatalf("L2 out of bounds: %.4f", l2)
	}
}

func TestCMS_EmptyAndUnknownKeys(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	for _, k := range []string{"a", "b", "c"} {
		in := common.FromString(k)
		c, _ := s.QueryWithHash(common.QueryFrequency, in.Hash)
		if c != 0 {
			t.Fatalf("empty sketch should return 0 for key=%s", k)
		}
	}
}

func TestCMS_Determinism(t *testing.T) {
	s1 := newTestCMS(t, CM_ROW_NO, CM_COL_NO)
	s2 := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	keys := []string{"a", "a", "b", "c", "c", "c"}

	for _, k := range keys {
		in := common.FromString(k)
		s1.InsertWithHash(in.Hash)
		s2.InsertWithHash(in.Hash)
	}

	for _, k := range []string{"a", "b", "c", "z"} {
		in := common.FromString(k)
		c1, _ := s1.QueryWithHash(common.QueryFrequency, in.Hash)
		c2, _ := s2.QueryWithHash(common.QueryFrequency, in.Hash)
		if c1 != c2 {
			t.Fatalf("determinism failed for key=%s", k)
		}
	}
}

func TestCMS_Monotonicity(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)
	in := common.FromString("mono")

	prevC := 0.0
	prevS := 0.0
	prevS2 := 0.0

	for i := 0; i < 10; i++ {
		s.InsertWithHash(in.Hash)

		c, _ := s.QueryWithHash(common.QueryFrequency, in.Hash)
		su, _ := s.QueryWithHash(common.QuerySum, in.Hash)
		s2, _ := s.QueryWithHash(common.QuerySum2, in.Hash)

		if c < prevC {
			t.Fatal("count decreased")
		}
		if su < prevS {
			t.Fatal("sum decreased")
		}
		if s2 < prevS2 {
			t.Fatal("sum2 decreased")
		}

		prevC, prevS, prevS2 = c, su, s2
	}
}

func TestCMS_L1L2_AccountingMany(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	N := 500
	for i := 0; i < N; i++ {
		in := common.FromString(fmt.Sprintf("k_%03d", i%37))
		s.InsertWithHash(in.Hash)
	}

	l1 := s.CM_L1()
	if math.Abs(l1-float64(N)) > 1e-9 {
		t.Fatalf("L1 expected %d, got %.2f", N, l1)
	}

	l2 := s.CM_L2()
	if l2 < math.Sqrt(float64(N)) || l2 > float64(N) {
		t.Fatalf("L2 out of bounds: %.4f", l2)
	}
}

func TestCMS_NoUnderestimateOnTrackedKeys(t *testing.T) {
	s := newTestCMS(t, CM_ROW_NO, CM_COL_NO)

	truth := map[string]int{
		"hotA": 0,
		"hotB": 0,
		"hotC": 0,
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
			k = fmt.Sprintf("cold_%d", i)
		}

		in := common.FromString(k)
		s.InsertWithHash(in.Hash)

		if _, ok := truth[k]; ok {
			truth[k]++
		}
	}

	for k, cnt := range truth {
		in := common.FromString(k)
		est, _ := s.QueryWithHash(common.QueryFrequency, in.Hash)
		if est < float64(cnt) {
			t.Fatalf("underestimate key=%s est=%.2f true=%d", k, est, cnt)
		}
	}
}
