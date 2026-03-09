package storage

import (
	"encoding/json"
	"testing"
)

func TestVector1D_BasicOps(t *testing.T) {
	traceTest(t)
	v, err := InitVector1D[float64](8)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if v.Len() != 0 {
		t.Fatalf("unexpected len: %d", v.Len())
	}

	filled, err := FilledVector1D(3, 2.0)
	if err != nil {
		t.Fatalf("filled: %v", err)
	}
	if filled.Len() != 3 {
		t.Fatalf("unexpected filled len: %d", filled.Len())
	}

	filled.Fill(5.0)
	for i, x := range filled.Iter() {
		if x != 5 {
			t.Fatalf("fill mismatch at %d: got=%v want=5", i, x)
		}
	}
}

func TestVector1D_InsertAndConditionalUpdate(t *testing.T) {
	traceTest(t)
	v := Vector1DFromSlice([]int{1, 3, 5})
	if err := v.Insert(1, 2); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got := v.AsSlice()
	want := []int{1, 2, 3, 5}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("insert mismatch at %d: got=%d want=%d", i, got[i], want[i])
		}
	}

	if err := v.UpdateIfGreater(0, 9); err != nil {
		t.Fatalf("UpdateIfGreater: %v", err)
	}
	if err := v.UpdateIfSmaller(1, -1); err != nil {
		t.Fatalf("UpdateIfSmaller: %v", err)
	}
	if v.AsSlice()[0] != 9 || v.AsSlice()[1] != -1 {
		t.Fatalf("conditional update failed: %+v", v.AsSlice())
	}
}

func TestVector1D_SafeAccessAndMutation(t *testing.T) {
	traceTest(t)
	v := Vector1DFromVec([]int{10, 20, 30})

	if v.IsEmpty() {
		t.Fatalf("vector should not be empty")
	}
	p, err := v.Get(1)
	if err != nil || *p != 20 {
		t.Fatalf("Get failed: err=%v val=%v", err, *p)
	}
	pm, err := v.GetMut(1)
	if err != nil {
		t.Fatalf("GetMut failed: %v", err)
	}
	*pm = 25
	last, err := v.LastMut()
	if err != nil {
		t.Fatalf("LastMut failed: %v", err)
	}
	*last = 35
	if got := v.AsSlice(); got[1] != 25 || got[2] != 35 {
		t.Fatalf("mutation via pointer failed: %+v", got)
	}
}

func TestVector1D_PushTruncateClearAppendExtend(t *testing.T) {
	traceTest(t)
	v := Vector1DFromVec([]int{1, 2})
	v.Push(3)
	v.ExtendFromSlice([]int{4, 5})
	if got := v.AsSlice(); len(got) != 5 || got[4] != 5 {
		t.Fatalf("push/extend failed: %+v", got)
	}

	v.Truncate(3)
	if got := v.AsSlice(); len(got) != 3 || got[2] != 3 {
		t.Fatalf("truncate failed: %+v", got)
	}

	other := Vector1DFromVec([]int{7, 8})
	v.Append(other)
	if len(other.AsSlice()) != 0 {
		t.Fatalf("append should clear source vector")
	}
	if got := v.AsSlice(); len(got) != 5 || got[3] != 7 || got[4] != 8 {
		t.Fatalf("append failed: %+v", got)
	}

	v.Clear()
	if !v.IsEmpty() {
		t.Fatalf("clear failed")
	}
}

func TestVector1D_SwapSortAndUpdateOneCounter(t *testing.T) {
	traceTest(t)
	v := Vector1DFromVec([]int{3, 1, 2})
	if err := v.Swap(0, 2); err != nil {
		t.Fatalf("swap failed: %v", err)
	}
	if got := v.AsSlice(); got[0] != 2 || got[2] != 3 {
		t.Fatalf("swap mismatch: %+v", got)
	}

	if err := UpdateOneCounterTyped(v, 1, func(target *int, delta int) {
		*target += delta
	}, 10); err != nil {
		t.Fatalf("UpdateOneCounter failed: %v", err)
	}
	if got := v.AsSlice()[1]; got != 11 {
		t.Fatalf("UpdateOneCounter mismatch: got=%d want=11", got)
	}

	v.SortBy(func(a, b int) int { return a - b })
	if got := v.AsSlice(); got[0] != 2 || got[1] != 3 || got[2] != 11 {
		t.Fatalf("SortBy failed: %+v", got)
	}
}

func TestVector1D_JSONRoundTrip(t *testing.T) {
	traceTest(t)
	in := Vector1DFromVec([]int{4, 5, 6})
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var out Vector1D[int]
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	got := out.AsSlice()
	if len(got) != 3 || got[0] != 4 || got[2] != 6 {
		t.Fatalf("json round-trip mismatch: %+v", got)
	}
}
