package storage

import "testing"

func TestVector3D_Basic(t *testing.T) {
	traceTest(t)
	v, err := InitVector3D[float64](2, 3, 4)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if v.Layers() != 2 || v.Rows() != 3 || v.Cols() != 4 {
		t.Fatalf("unexpected dimensions: %d %d %d", v.Layers(), v.Rows(), v.Cols())
	}

	v.Set(1, 2, 3, 7.5)
	if got := v.At(1, 2, 3); got != 7.5 {
		t.Fatalf("unexpected value: got=%v want=7.5", got)
	}
}
