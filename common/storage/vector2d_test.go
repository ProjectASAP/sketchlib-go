package storage

import "testing"

func TestFlatVector2D_BasicAccess(t *testing.T) {
	traceTest(t)
	m, err := NewFlatVector2D(3, 4)
	if err != nil {
		t.Fatalf("new flat matrix: %v", err)
	}

	m.Set(1, 2, 7)
	m.Add(1, 2, 3)
	if got := m.At(1, 2); got != 10 {
		t.Fatalf("unexpected value: got=%v want=10", got)
	}

	view := m.As2D()
	view[1][2] = 13
	if got := m.At(1, 2); got != 13 {
		t.Fatalf("2D view must share backing storage: got=%v want=13", got)
	}
}

func TestFlatVector2D_From2D(t *testing.T) {
	traceTest(t)
	src := [][]float64{
		{1, 2, 3},
		{4, 5, 6},
	}
	m, err := NewFlatVector2DFrom2D(src)
	if err != nil {
		t.Fatalf("from2d: %v", err)
	}

	if m.Rows() != 2 || m.Cols() != 3 {
		t.Fatalf("unexpected dimensions rows=%d cols=%d", m.Rows(), m.Cols())
	}
	if got := m.At(1, 1); got != 5 {
		t.Fatalf("unexpected copied value: got=%v want=5", got)
	}
}

func TestVector2D_SerializeRoundTrip(t *testing.T) {
	traceTest(t)
	m, err := Vector2DFromFn[float64](2, 3, func(r, c int) float64 {
		return float64(r*10 + c)
	})
	if err != nil {
		t.Fatalf("from_fn: %v", err)
	}

	data, err := m.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	out, err := DeserializeVector2DFromBytes[float64](data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	if out.Rows() != 2 || out.Cols() != 3 {
		t.Fatalf("metadata mismatch after round-trip")
	}
	if out.At(1, 2) != 12 {
		t.Fatalf("value mismatch after round-trip: got=%v want=12", out.At(1, 2))
	}
}

func TestVector2D_RowSliceAndIndex(t *testing.T) {
	traceTest(t)
	m, err := InitVector2D[int](2, 4)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	m.Set(1, 2, 99)

	if idx := m.Index(1, 2); idx != 6 {
		t.Fatalf("index mismatch: got=%d want=6", idx)
	}
	row := m.RowSlice(1)
	if len(row) != 4 || row[2] != 99 {
		t.Fatalf("rowslice mismatch: %+v", row)
	}
	row[2] = 77
	if m.At(1, 2) != 77 {
		t.Fatalf("rowslice must be zero-copy view")
	}
}

func TestVector2D_FastOps(t *testing.T) {
	traceTest(t)
	m, err := InitVector2D[float64](3, 8)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	colFn := func(row int, maskBits uint, mask uint64) int {
		_ = maskBits
		return int((uint64(row*3) + 1) & mask)
	}

	m.FastInsert(2.0, colFn, nil)
	minVal := m.FastQueryMin(colFn)
	if minVal != 2.0 {
		t.Fatalf("fast min mismatch: got=%v want=2", minVal)
	}

	median := m.FastQueryMedian(colFn, nil)
	if median != 2.0 {
		t.Fatalf("fast median mismatch: got=%v want=2", median)
	}

	sum := m.FastQueryAggregate(colFn, 0.0, func(acc float64, _ int, v float64) float64 {
		return acc + v
	})
	if sum != 6.0 {
		t.Fatalf("fast aggregate mismatch: got=%v want=6", sum)
	}
}
