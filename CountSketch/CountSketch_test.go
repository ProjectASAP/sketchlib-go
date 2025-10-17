package countsketch

import (
	"fmt"
	"testing"
)

// --- Helpers -----------------------------------------------------------------

// Build deterministic seeds for positions (seed1) and signs (seed2).
// We avoid time/rand to keep tests reproducible.
func makeDeterministicSeeds(rows int) (seed1, seed2 []uint32) {
	seed1 = make([]uint32, rows)
	seed2 = make([]uint32, rows)
	// Use two different linear congruential-ish sequences with mixed constants.
	for i := 0; i < rows; i++ {
		seed1[i] = 0x9e3779b9 ^ uint32(i*2654435761) ^ uint32(0x85ebca6b)
		seed2[i] = 0x85ebca6b ^ uint32(i*2246822519) ^ uint32(0xc2b2ae35)
	}
	return
}

func newTestCS(t *testing.T, rows, cols int) *CountSketch {
	seed1, seed2 := makeDeterministicSeeds(rows)
	cs, err := NewCountSketch(rows, cols, seed1, seed2)
	if err != nil {
		t.Fatalf("NewCountSketch error: %v", err)
	}
	return cs
}

func approxInt64Equal(a, b, tol int64) bool {
	if a > b {
		return a-b <= tol
	}
	return b-a <= tol
}

// --- Tests -------------------------------------------------------------------

// Core behavior: UpdateAndEstimateString should track (approximately) the true count.
// CountSketch is an unbiased estimator; with large tables and few keys, error is usually small.
func TestCS_UpdateAndEstimate_Basic(t *testing.T) {
	cs := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	type sample struct {
		key   string
		vals  []int64
		total int64
	}
	data := []sample{
		{"alpha", []int64{10, 20, -5, 7}, 32},
		{"beta", []int64{3, 3, 3}, 9},
		{"gamma", []int64{100}, 100},
		{"delta", []int64{-4, -6, 2}, -8},
	}
	for _, smp := range data {
		sum := int64(0)
		for _, v := range smp.vals {
			sum += v
			// Track estimate along the way (function returns float64 median).
			_ = cs.UpdateAndEstimateString(smp.key, float64(v))
		}
		if sum != smp.total {
			t.Fatalf("internal sanity: expected total %d, got %d", smp.total, sum)
		}
	}

	// Check final estimates
	// Tolerance: allow small absolute error due to collisions (±3).
	const tol int64 = 3
	for _, smp := range data {
		est := cs.EstimateStringCount(smp.key)
		if !approxInt64Equal(est, smp.total, tol) {
			t.Fatalf("Estimate mismatch for key=%s: est=%d, true=%d (tol=%d)",
				smp.key, est, smp.total, tol)
		}
	}
}

// Repeated updates using UpdateString (which increments by sign only).
// Even though UpdateString ignores the "count" parameter and increments ±1 per update,
// repeated updates on the same key should still increase the median estimate roughly linearly.
func TestCS_UpdateString_Repeated(t *testing.T) {
	cs := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	key := "repeated"
	reps := 200
	for i := 0; i < reps; i++ {
		cs.UpdateString(key, 1000 /* ignored; UpdateString adds only ±1 per row */)
	}
	est := cs.EstimateStringCount(key)

	// With 3 rows and large table, collisions are rare; expect near reps.
	// Use a conservative tolerance.
	const tol int64 = 10
	if !approxInt64Equal(est, int64(reps), tol) {
		t.Fatalf("UpdateString estimate off: est=%d, true≈%d (tol=%d)", est, reps, tol)
	}
}

// Determinism: with fixed seeds and same updates, two sketches must match exactly.
func TestCS_DeterminismWithFixedSeeds(t *testing.T) {
	seed1, seed2 := makeDeterministicSeeds(CS_ROW_NO_Univ_ELEPHANT)
	cs1, err := NewCountSketch(CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT, seed1, seed2)
	if err != nil {
		t.Fatalf("NewCountSketch err: %v", err)
	}
	cs2, err := NewCountSketch(CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT, seed1, seed2)
	if err != nil {
		t.Fatalf("NewCountSketch err: %v", err)
	}

	feed := []struct {
		key string
		val int64
	}{
		{"a", 1}, {"a", 2},
		{"b", 10},
		{"c", -3}, {"c", 7},
		{"d", -11},
	}
	for _, x := range feed {
		cs1.UpdateAndEstimateString(x.key, float64(x.val))
		cs2.UpdateAndEstimateString(x.key, float64(x.val))
	}

	for _, k := range []string{"a", "b", "c", "d", "z"} {
		if cs1.EstimateStringCount(k) != cs2.EstimateStringCount(k) {
			t.Fatalf("Determinism fail for key=%s", k)
		}
	}
}

// L1/L2 sanity checks under many inserts.
// We do not assert tight bounds because CS keeps signed counts; instead we check coarse invariants.
func TestCS_L1L2_SanityUnderLoad(t *testing.T) {
	cs := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	N := 1500
	for i := 0; i < N; i++ {
		k := fmt.Sprintf("k_%03d", i%41) // 41 unique keys
		v := float64((i % 9) - 4)        // values in [-4..4]
		// Mix: sometimes signed unit updates, sometimes value updates
		if i%3 == 0 {
			cs.UpdateString(k, v)
		} else {
			cs.UpdateAndEstimateString(k, v)
		}
	}

	l1 := cs.cs_l1()
	l2 := cs.cs_l2()

	// L1 should be non-negative and reasonably bounded.
	if l1 < 0 {
		t.Fatalf("cs_l1 negative: %.2f", l1)
	}
	// L2 should be positive and not explode.
	if !(l2 > 0) {
		t.Fatalf("cs_l2 should be > 0")
	}
	// Very loose upper bound: l2 should be far less than, say, 10*N in this workload.
	if l2 > float64(10*N) {
		t.Fatalf("cs_l2 too large: %.2f (N=%d)", l2, N)
	}
}

// Heavy-hitter signal: hot keys should have larger estimates than cold keys on average.
func TestCS_HeavyHitterSignal(t *testing.T) {
	cs := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	// Hot keys receive many more updates.
	hot := []string{"hotA", "hotB", "hotC"}
	coldCount := 0

	total := 3000
	for i := 0; i < total; i++ {
		switch {
		case i%5 == 0:
			cs.UpdateAndEstimateString("hotA", 2)
		case i%7 == 0:
			cs.UpdateAndEstimateString("hotB", 3)
		case i%11 == 0:
			cs.UpdateAndEstimateString("hotC", 1)
		default:
			cs.UpdateAndEstimateString(fmt.Sprintf("cold_%d", i), 1)
			coldCount++
		}
	}

	// Compare average hot estimate vs. a sample of cold keys.
	hotSum := int64(0)
	for _, k := range hot {
		hotSum += cs.EstimateStringCount(k)
	}
	hotAvg := float64(hotSum) / float64(len(hot))

	// Sample 50 cold keys (or all, if fewer)
	sample := 50
	if coldCount < sample {
		sample = coldCount
	}
	coldSum := int64(0)
	collected := 0
	for i := 0; i < total && collected < sample; i++ {
		k := fmt.Sprintf("cold_%d", i)
		coldSum += cs.EstimateStringCount(k)
		collected++
	}
	coldAvg := float64(coldSum) / float64(sample)

	if !(hotAvg > coldAvg) {
		t.Fatalf("heavy-hitter signal missing: hotAvg=%.2f, coldAvg=%.2f", hotAvg, coldAvg)
	}
}

// Merge correctness: merging two sketches should roughly sum their estimates,
// and heavy hitters from both sides should surface in the merged TopK.
func TestCS_MergeWith(t *testing.T) {
	csA := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)
	csB := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	// A's workload
	for i := 0; i < 500; i++ { // hh1 total +1000
		csA.UpdateAndEstimateString("hh1", 2)
	}
	for i := 0; i < 300; i++ { // hh2 total +900
		csA.UpdateAndEstimateString("hh2", 3)
	}
	for i := 0; i < 200; i++ {
		csA.UpdateAndEstimateString(fmt.Sprintf("coldA_%d", i), 1)
	}

	// B's workload
	for i := 0; i < 400; i++ { // hh1 additional +400
		csB.UpdateAndEstimateString("hh1", 1)
	}
	for i := 0; i < 350; i++ { // hh3 total +1400
		csB.UpdateAndEstimateString("hh3", 4)
	}
	for i := 200; i < 400; i++ {
		csB.UpdateAndEstimateString(fmt.Sprintf("coldB_%d", i), 1)
	}

	// Merge B into A
	csA.MergeWith(csB)

	// Expected totals after merge (approximate due to sketching)
	expect := map[string]int64{
		"hh1": 1000 + 400, // 1400
		"hh2": 900,
		"hh3": 1400,
	}

	const tol int64 = 40 // generous absolute tolerance for collisions
	for k, v := range expect {
		got := csA.EstimateStringCount(k)
		if !approxInt64Equal(got, v, tol) {
			t.Fatalf("after merge: key=%s est=%d, true≈%d (tol=%d)", k, got, v, tol)
		}
	}

	// Check that the merged TopK contains heavy hitters (order inside heap is implementation-specific).
	// We'll assert that hh1 and hh3 appear with estimates larger than a typical cold key.
	hh1 := csA.EstimateStringCount("hh1")
	hh3 := csA.EstimateStringCount("hh3")
	cold := csA.EstimateStringCount("coldA_0") + csA.EstimateStringCount("coldB_200")
	if !(hh1 > cold && hh3 > cold) {
		t.Fatalf("TopK/est signal weak after merge: hh1=%d hh3=%d cold≈%d", hh1, hh3, cold)
	}
}

// TopKHeap ordering signal: the top-k structure should surface larger keys.
// We don't rely on internal heap order; we sort a snapshot and assert ranking.
func TestCS_TopKHeapOrdering(t *testing.T) {
	cs := newTestCS(t, CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)

	// Create clearly separated magnitudes
	type kv struct {
		key   string
		total int
	}
	keys := []kv{
		{"k5", 500},
		{"k4", 400},
		{"k3", 300},
		{"k2", 200},
		{"k1", 100},
	}
	for _, it := range keys {
		for i := 0; i < it.total; i++ {
			cs.UpdateAndEstimateString(it.key, 1)
		}
	}
	// Some noise keys
	for i := 0; i < 300; i++ {
		cs.UpdateAndEstimateString(fmt.Sprintf("noise_%d", i), 1)
	}

	// Snapshot TopK entries (heap internal order may be arbitrary).
	type entry struct {
		key   string
		count int64
	}
	var ents []entry
	for _, e := range cs.topK.heap {
		ents = append(ents, entry{key: e.key, count: e.count})
	}
	if len(ents) == 0 {
		t.Fatalf("TopK heap is empty")
	}

	// Build a map for easy lookup
	got := map[string]int64{}
	for _, e := range ents {
		got[e.key] = e.count
	}

	// Expect all five to be present with descending magnitudes.
	for _, it := range keys {
		if _, ok := got[it.key]; !ok {
			t.Fatalf("TopK missing key %s", it.key)
		}
	}

	// Check monotone signal: k5 >= k4 >= k3 >= k2 >= k1 (with tolerances)
	k := func(s string) int64 { return got[s] }
	if !(k("k5") >= k("k4") && k("k4") >= k("k3") && k("k3") >= k("k2") && k("k2") >= k("k1")) {
		t.Fatalf("TopK ranking signal weak: k5=%d k4=%d k3=%d k2=%d k1=%d",
			k("k5"), k("k4"), k("k3"), k("k2"), k("k1"))
	}

	// Sanity: top items should be significantly larger than a typical noise key.
	noise := cs.EstimateStringCount("noise_0")
	if !(k("k5") > noise && k("k4") > noise && k("k3") > noise) {
		t.Fatalf("TopK insufficiently separates hot vs noise: k5=%d k4=%d k3=%d noise=%d",
			k("k5"), k("k4"), k("k3"), noise)
	}
}
