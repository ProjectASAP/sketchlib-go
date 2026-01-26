package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
)

func logCMS(t *testing.T, name string, expected, estimated float64) {
	t.Helper()
	t.Logf("[%s] expected>=%.2f estimated=%.2f", name, expected, estimated)
}

// 1. Zero-state correctness
func TestCMS_ZeroState(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	h := common.FromString("never").Hash
	est, _ := s.QueryWithHash(common.QueryFrequency, h)

	logCMS(t, "ZeroState", 0, est)

	if est != 0 {
		t.Fatalf("non-zero on empty sketch")
	}
}

// 2. No-underestimation (CORE CMS PROPERTY)
func TestCMS_NoUnderestimate(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	for i := 0; i < 500; i++ {
		s.InsertWithHash(common.FromString("hot").Hash)
	}

	est, _ := s.QueryWithHash(common.QueryFrequency, common.FromString("hot").Hash)
	logCMS(t, "NoUnderestimate", 500, est)

	if est < 500 {
		t.Fatalf("CMS underestimation detected")
	}
}

// 3. Linearity
func TestCMS_Linearity(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	for i := 0; i < 200; i++ {
		s.InsertWithHash(common.FromString("x").Hash)
	}
	for i := 0; i < 300; i++ {
		s.InsertWithHash(common.FromString("x").Hash)
	}

	est, _ := s.QueryWithHash(common.QueryFrequency, common.FromString("x").Hash)
	logCMS(t, "Linearity", 500, est)

	if est < 500 {
		t.Fatalf("linearity violated")
	}
}

// 4. Monotonicity
func TestCMS_Monotonicity(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	h := common.FromString("mono").Hash

	prev := 0.0
	for i := 0; i < 10; i++ {
		s.InsertWithHash(h)
		cur, _ := s.QueryWithHash(common.QueryFrequency, h)

		t.Logf("[Monotonicity] step=%d val=%.2f", i, cur)

		if cur < prev {
			t.Fatalf("count decreased")
		}
		prev = cur
	}
}

// 5. Determinism
func TestCMS_Determinism(t *testing.T) {
	s1, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	s2, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	keys := []string{"a", "a", "b", "c", "c", "c"}

	for _, k := range keys {
		h := common.FromString(k).Hash
		s1.InsertWithHash(h)
		s2.InsertWithHash(h)
	}

	for _, k := range []string{"a", "b", "c"} {
		h := common.FromString(k).Hash
		c1, _ := s1.QueryWithHash(common.QueryFrequency, h)
		c2, _ := s2.QueryWithHash(common.QueryFrequency, h)

		t.Logf("[Determinism] key=%s %.2f %.2f", k, c1, c2)

		if c1 != c2 {
			t.Fatalf("non-deterministic result")
		}
	}
}

// 6. Merge correctness
func TestCMS_MergeCorrectness(t *testing.T) {
	s1, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	s2, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	for i := 0; i < 400; i++ {
		s1.InsertWithHash(common.FromString("m").Hash)
	}
	for i := 0; i < 600; i++ {
		s2.InsertWithHash(common.FromString("m").Hash)
	}

	if err := s1.Merge(s2); err != nil {
		t.Fatalf("merge failed")
	}

	est, _ := s1.QueryWithHash(common.QueryFrequency, common.FromString("m").Hash)
	logCMS(t, "Merge", 1000, est)

	if est < 1000 {
		t.Fatalf("merge underestimation")
	}
}

// 7. L1 & L2 accounting sanity
func TestCMS_L1L2(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)

	N := 300
	for i := 0; i < N; i++ {
		s.InsertWithHash(common.FromString(fmt.Sprintf("k%d", i%10)).Hash)
	}

	l1 := s.CM_L1()
	l2 := s.CM_L2()

	t.Logf("[L1/L2] L1=%.2f L2=%.2f", l1, l2)

	if math.Abs(l1-float64(N)) > 1e-9 {
		t.Fatalf("L1 incorrect")
	}
	if l2 < math.Sqrt(float64(N)) || l2 > float64(N) {
		t.Fatalf("L2 out of bounds")
	}
}

// 8. Query purity
func TestCMS_QueryNoSideEffect(t *testing.T) {
	s, _ := NewCountMinSketch(CM_ROW_NO, CM_COL_NO)
	h := common.FromString("pure").Hash

	s.InsertWithHash(h)
	before, _ := s.QueryWithHash(common.QueryFrequency, h)

	for i := 0; i < 100; i++ {
		s.QueryWithHash(common.QueryFrequency, h)
	}

	after, _ := s.QueryWithHash(common.QueryFrequency, h)
	logCMS(t, "QueryPurity", before, after)

	if before != after {
		t.Fatalf("query mutated state")
	}
}
