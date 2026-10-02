package univmon

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func mustNew[K string | int64 | uint64](t *testing.T, heapSize, row, col, layers int) *UnivMon[K] {
	t.Helper()
	u, err := NewUnivMon[K](heapSize, row, col, layers)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustInsert[K string | int64 | uint64](t *testing.T, u *UnivMon[K], key K, w int64) {
	t.Helper()
	if err := u.Insert(key, w); err != nil {
		t.Fatal(err)
	}
}

func heapCount[K string | int64 | uint64](u *UnivMon[K], layer int, key K) (int64, bool) {
	for _, e := range u.HeapEntries(layer) {
		if e.Key == key {
			return e.Count, true
		}
	}
	return 0, false
}

func TestNewUnivMonRejectsShapes(t *testing.T) {
	for _, c := range []struct{ heap, row, col, layers int }{
		{0, 3, 16, 4}, {4, 0, 16, 4}, {4, 3, 0, 4}, {4, 3, 16, 0},
		{4, 3, 16, MaxLayerSize + 1}, {4, MaxRows + 1, 16, 4},
		{4, 20, 64, 4}, // (6 column bits + 1 sign bit) * 20 rows > 128
	} {
		if _, err := NewUnivMon[string](c.heap, c.row, c.col, c.layers); err == nil {
			t.Errorf("NewUnivMon(%d, %d, %d, %d) succeeded", c.heap, c.row, c.col, c.layers)
		}
	}
	for _, c := range []struct{ heap, row, col, layers int }{
		{1, 1, 1, 1}, {4, MaxRows, 16, MaxLayerSize}, {4, 16, 128, 4},
	} {
		if _, err := NewUnivMon[string](c.heap, c.row, c.col, c.layers); err != nil {
			t.Errorf("NewUnivMon(%d, %d, %d, %d): %v", c.heap, c.row, c.col, c.layers, err)
		}
	}
}

func TestBottomLayerForHash(t *testing.T) {
	// The bottom layer is the number of consecutive set bits from bit 1 up,
	// capped at layerSize-1.
	for _, c := range []struct {
		hash   uint64
		layers int
		want   int
	}{
		{0, 8, 0}, {1, 8, 0}, {0b10, 8, 1}, {0b110, 8, 2}, {0b1011110, 8, 4},
		{math.MaxUint64, 8, 7}, {math.MaxUint64, 64, 63}, {0b110, 1, 0}, {0b110, 2, 1},
	} {
		if got := BottomLayerForHash(c.hash, c.layers); got != c.want {
			t.Errorf("BottomLayerForHash(%#b, %d) = %d, want %d", c.hash, c.layers, got, c.want)
		}
	}
}

func TestInsertTracksWeightAndHeavyHitter(t *testing.T) {
	u := mustNew[string](t, 16, 3, 32, 4)
	for range 40 {
		mustInsert(t, u, "alpha", 1)
	}
	if u.BucketSize() != 40 || u.CalcL1() != 40 {
		t.Errorf("weight %d, L1 %v, want 40", u.BucketSize(), u.CalcL1())
	}
	if c, ok := heapCount(u, 0, "alpha"); !ok || c != 40 {
		t.Errorf("layer 0 holds alpha at %d (present %v), want 40", c, ok)
	}
	if got := u.CalcCard(); got != 1 {
		t.Errorf("cardinality %v, want 1", got)
	}
}

func TestInsertRules(t *testing.T) {
	u := mustNew[string](t, 4, 3, 16, 4)
	if err := u.Insert("a", -1); err == nil {
		t.Error("negative weight accepted")
	}
	if u.Mode() != UpdateModeUnset || u.BucketSize() != 0 {
		t.Error("a rejected insert changed the pyramid")
	}
	mustInsert(t, u, "a", 0)
	if u.Mode() != UpdateModeStandard {
		t.Errorf("mode %d after Insert, want standard", u.Mode())
	}
	if err := u.FastInsert("a", 1); err == nil {
		t.Error("FastInsert into a standard pyramid accepted")
	}
	v := mustNew[string](t, 4, 3, 16, 4)
	if err := v.FastInsert("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := v.Insert("a", 1); err == nil {
		t.Error("Insert into a terminal pyramid accepted")
	}
	if err := u.Merge(v); err == nil {
		t.Error("standard and terminal pyramids merged")
	}
	if err := u.Merge(mustNew[string](t, 4, 3, 16, 5)); err == nil {
		t.Error("pyramids of different depth merged")
	}
	if err := u.Merge(mustNew[string](t, 5, 3, 16, 4)); err == nil {
		t.Error("pyramids of different heap size merged")
	}
	u.bucketSize = math.MaxUint64
	if err := u.Insert("a", 1); err == nil {
		t.Error("weight overflow accepted")
	}
}

func TestStandardInsertUpdatesEveryReachedLayer(t *testing.T) {
	u := mustNew[uint64](t, 16, 3, 128, 6)
	var key uint64
	for BottomLayerForHash(bottomLayerHash(keyID(key)), u.layerSize) < 2 {
		key++
	}
	bottom := BottomLayerForHash(bottomLayerHash(keyID(key)), u.layerSize)
	mustInsert(t, u, key, 3)
	for level := range u.layerSize {
		want := 0.0
		if level <= bottom {
			want = 3
		}
		if got := u.LayerL2(level); got != want {
			t.Errorf("layer %d L2 %v, want %v (bottom %d)", level, got, want, bottom)
		}
	}
}

func TestFastInsertTouchesOneLayer(t *testing.T) {
	u := mustNew[uint64](t, 16, 3, 128, 6)
	var key uint64
	for BottomLayerForHash(bottomLayerHash(keyID(key)), u.layerSize) < 2 {
		key++
	}
	bottom := BottomLayerForHash(bottomLayerHash(keyID(key)), u.layerSize)
	if err := u.FastInsert(key, 3); err != nil {
		t.Fatal(err)
	}
	for level := range u.layerSize {
		want := 0.0
		if level == bottom {
			want = 3
		}
		if got := u.LayerL2(level); got != want {
			t.Errorf("layer %d L2 %v, want %v", level, got, want)
		}
	}
	if u.CalcL1() != 3 || u.CalcL2() != 3 || u.CalcCard() != 1 || u.CalcEntropy() != 0 {
		t.Errorf("L1 %v L2 %v card %v entropy %v, want 3 3 1 0", u.CalcL1(), u.CalcL2(), u.CalcCard(), u.CalcEntropy())
	}
}

func TestSmallStreamCardinalityAndL1(t *testing.T) {
	u := mustNew[string](t, 100, 3, 2048, 16)
	for _, c := range []struct {
		key string
		w   int64
	}{
		{"notfound", 1}, {"hello", 1}, {"count", 3}, {"min", 4}, {"world", 10},
		{"cheatcheat", 3}, {"cheatcheat", 7}, {"min", 2}, {"hello", 2}, {"tigger", 34},
		{"flow", 9}, {"miss", 4}, {"hello", 30}, {"world", 10}, {"hello", 10}, {"mom", 1},
	} {
		mustInsert(t, u, c.key, c.w)
	}
	if u.CalcCard() != 10 || u.CalcL1() != 131 {
		t.Errorf("cardinality %v, L1 %v, want 10 and 131", u.CalcCard(), u.CalcL1())
	}
}

func TestMergeCombinesHeavyHitters(t *testing.T) {
	left := mustNew[string](t, 16, 3, 32, 4)
	right := mustNew[string](t, 16, 3, 32, 4)
	for range 25 {
		mustInsert(t, left, "left", 1)
	}
	for range 30 {
		mustInsert(t, right, "right", 1)
	}
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	if c, _ := heapCount(left, 0, "left"); c != 25 {
		t.Errorf("left holds left at %d, want 25", c)
	}
	if c, _ := heapCount(left, 0, "right"); c != 30 {
		t.Errorf("left holds right at %d, want 30", c)
	}
	if left.BucketSize() != 55 {
		t.Errorf("weight %d, want 55", left.BucketSize())
	}
}

func TestMergeRebuildsL2AndEvictedCandidates(t *testing.T) {
	left := mustNew[string](t, 1, 3, 1024, 1)
	right := mustNew[string](t, 1, 3, 1024, 1)
	mustInsert(t, left, "x", 100)
	mustInsert(t, right, "x", 5)
	mustInsert(t, right, "y", 10)
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	if left.BucketSize() != 115 {
		t.Errorf("weight %d, want 115", left.BucketSize())
	}
	if want := math.Sqrt(105*105 + 10*10); math.Abs(left.LayerL2(0)-want) > 1e-9 {
		t.Errorf("L2 %v, want %v", left.LayerL2(0), want)
	}
	if c, ok := heapCount(left, 0, "x"); !ok || c != 105 {
		t.Errorf("x at %d (present %v), want 105", c, ok)
	}
	if left.CandidatesComplete()[0] {
		t.Error("two candidates for a heap of one reported complete")
	}
	if e := left.CalcEntropy(); math.IsNaN(e) || math.IsInf(e, 0) || e < 0 {
		t.Errorf("entropy %v", e)
	}

	a := mustNew[string](t, 1, 3, 1024, 1)
	b := mustNew[string](t, 1, 3, 1024, 1)
	mustInsert(t, a, "x", 1)
	mustInsert(t, b, "y", 2)
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if a.CandidatesComplete()[0] {
		t.Error("two complete single-key heaps merged into a complete heap of one")
	}
	if c, ok := heapCount(a, 0, "y"); !ok || c != 2 {
		t.Errorf("y at %d (present %v), want 2", c, ok)
	}
}

// TestMergedHalvesMatchOnePass splits a stream whose candidate sets stay
// complete: the merged halves hold the one-pass state exactly.
func TestMergedHalvesMatchOnePass(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		insert := func(u *UnivMon[uint64], key uint64) {
			var err error
			if terminal {
				err = u.FastInsert(key, 1)
			} else {
				err = u.Insert(key, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		one := mustNew[uint64](t, 128, 5, 2048, 10)
		left := mustNew[uint64](t, 128, 5, 2048, 10)
		right := mustNew[uint64](t, 128, 5, 2048, 10)
		for i := range uint64(2000) {
			insert(one, i%64)
			if i%2 == 0 {
				insert(left, i%64)
			} else {
				insert(right, i%64)
			}
		}
		if err := left.Merge(right); err != nil {
			t.Fatal(err)
		}
		want, _ := one.MarshalASAPv1()
		got, _ := left.MarshalASAPv1()
		if !bytes.Equal(got, want) {
			t.Errorf("terminal=%v: merged halves differ from one pass", terminal)
		}
	}
}

func TestFreeResets(t *testing.T) {
	u := mustNew[string](t, 4, 3, 16, 4)
	fresh, _ := u.MarshalASAPv1()
	for i := range 50 {
		mustInsert(t, u, fmt.Sprint(i), 2)
	}
	u.Free()
	got, _ := u.MarshalASAPv1()
	if !bytes.Equal(got, fresh) {
		t.Error("Free did not restore the empty pyramid")
	}
	if err := u.FastInsert("a", 1); err != nil {
		t.Errorf("FastInsert after Free: %v", err)
	}
}

func TestRandomStreamWithinTolerance(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	u := mustNew[string](t, 256, 6, 8192, 16)
	truth := map[string]int64{}
	for range 10_000 {
		key := fmt.Sprintf("key_%d", rng.IntN(5000))
		w := int64(rng.IntN(100) + 1)
		truth[key] += w
		mustInsert(t, u, key, w)
	}
	var l1, l2sq, ent float64
	for _, v := range truth {
		f := float64(v)
		l1 += f
		l2sq += f * f
		ent += f * math.Log2(f)
	}
	for _, c := range []struct {
		name           string
		got, want, tol float64
	}{
		{"cardinality", u.CalcCard(), float64(len(truth)), 0.07},
		{"l1", u.CalcL1(), l1, 0.05},
		{"l2", u.CalcL2(), math.Sqrt(l2sq), 0.05},
		{"entropy", u.CalcEntropy(), math.Log2(l1) - ent/l1, 0.05},
	} {
		if rel := math.Abs(c.got-c.want) / c.want; rel > c.tol {
			t.Errorf("%s: %v against %v, relative error %.3f > %.2f", c.name, c.got, c.want, rel, c.tol)
		}
	}
}

func TestCountL2HHCell(t *testing.T) {
	// The column is bits [row*b, (row+1)*b) of the 128-bit hash folded % cols,
	// b = ceil(log2(cols)), and the sign is bit 127-row.
	rng := rand.New(rand.NewPCG(3, 4))
	for _, cols := range []int{1, 2, 4, 5, 10, 16, 1000, 4096} {
		rows := min(MaxRows, 128/(int(colsMaskBits(cols))+1))
		s, err := NewCountL2HH(rows, cols, 0)
		if err != nil {
			t.Fatal(err)
		}
		b := 0
		for 1<<b < cols {
			b++
		}
		for range 50 {
			h := common.Hash128{Lo: rng.Uint64(), Hi: rng.Uint64()}
			full := new(big.Int).Lsh(new(big.Int).SetUint64(h.Hi), 64)
			full.Or(full, new(big.Int).SetUint64(h.Lo))
			for r := range rows {
				field := new(big.Int).Rsh(full, uint(r*b))
				field.And(field, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(b)), big.NewInt(1)))
				wantCol := int(field.Uint64() % uint64(cols))
				wantSign := int64(-1)
				if full.Bit(127-r) == 1 {
					wantSign = 1
				}
				if col, sign := s.cell(h, r); col != wantCol || sign != wantSign {
					t.Fatalf("cols %d row %d: cell (%d, %d), want (%d, %d)", cols, r, col, sign, wantCol, wantSign)
				}
			}
		}
	}
}

func TestCarryL2Clamps(t *testing.T) {
	for _, c := range []struct{ acc, old, next, want int64 }{
		{0, 0, 3, 9},
		{9, 3, 5, 25},
		{25, 5, 0, 0},
		{4, 5, 0, 0},                                // would go negative
		{math.MaxInt64, 0, 1, math.MaxInt64},        // past the top
		{0, 0, math.MinInt64, math.MaxInt64},        // 2^126
		{math.MaxInt64, math.MinInt64, 0, 0},        // MaxInt64 - 2^126
		{100, -3000000000, 3000000000, 100},         // equal squares
		{0, 0, 3037000499, 3037000499 * 3037000499}, // largest square below 2^63
	} {
		if got := carryL2(c.acc, c.old, c.next); got != c.want {
			t.Errorf("carryL2(%d, %d, %d) = %d, want %d", c.acc, c.old, c.next, got, c.want)
		}
	}
}

func TestHeapKeepsLargest(t *testing.T) {
	h := newHHHeap[string](3, 3)
	add := func(k string, c int64) bool { return h.update(k, keyID(k), c) }
	for _, c := range []struct {
		key   string
		count int64
		room  bool
	}{
		{"a", 5, true}, {"b", 1, true}, {"c", 3, true},
		{"d", 1, false}, // ties the minimum: turned away
		{"", 0, false},
		{"e", 2, false}, // displaces b
		{"c", 7, true},  // resident: rescored
		{"f", 0, false},
	} {
		if c.key == "" {
			if _, ok := h.pos[keyID("b")]; !ok {
				t.Fatalf("a count tying the minimum displaced it: %v", h.entries)
			}
			continue
		}
		if got := add(c.key, c.count); got != c.room {
			t.Errorf("update(%s, %d) = %v, want %v", c.key, c.count, got, c.room)
		}
	}
	want := map[string]int64{"a": 5, "c": 7, "e": 2}
	if len(h.entries) != len(want) {
		t.Fatalf("heap holds %v", h.entries)
	}
	for i, e := range h.entries {
		if want[e.Key] != e.Count || h.pos[h.ids[i]] != i {
			t.Fatalf("heap holds %v", h.entries)
		}
		if i > 0 && h.entries[(i-1)/2].Count > e.Count {
			t.Fatalf("heap order broken: %v", h.entries)
		}
	}
}
