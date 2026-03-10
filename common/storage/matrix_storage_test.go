package storage

import (
	"math"
	"testing"
)

func TestHashModeForMatrix(t *testing.T) {
	traceTest(t)
	if got := HashModeForMatrix(3, 2048); got != MatrixHashPacked64 {
		t.Fatalf("expected packed64, got %v", got)
	}
	if got := HashModeForMatrix(8, 2048); got != MatrixHashPacked128 {
		t.Fatalf("expected packed128, got %v", got)
	}
	if got := HashModeForMatrix(20, 2048); got != MatrixHashRows {
		t.Fatalf("expected rows mode, got %v", got)
	}
}

func TestBuildMatrixHash_RowHashAndSign(t *testing.T) {
	traceTest(t)
	h := BuildMatrixHash(0xF0F0F0F0F0F0F0F0, 5, 2048)
	maskBits := maskBitsForCols(2048)
	mask := uint64(2047)

	for r := 0; r < 5; r++ {
		col := h.RowHash(r, maskBits, mask)
		if col > mask {
			t.Fatalf("row hash out of range: row=%d col=%d", r, col)
		}
		sign := h.SignForRow(r)
		if sign != -1 && sign != 1 {
			t.Fatalf("invalid sign at row=%d sign=%d", r, sign)
		}
	}
}

func TestDenseMatrixStorage_FastInsertAndQuery(t *testing.T) {
	traceTest(t)
	s, err := NewDenseMatrixStorage(5, 2048)
	if err != nil {
		t.Fatalf("new dense storage: %v", err)
	}

	baseHash := uint64(0xABCDEF1234567890)
	h := s.HashForMatrix(baseHash)

	for i := 0; i < 100; i++ {
		s.FastInsert(h, 1)
	}

	minVal := s.FastQueryMin(h)
	if minVal != 100 {
		t.Fatalf("expected min 100, got %v", minVal)
	}
}

func TestDenseMatrixStorage_FastQueryMedianSigned(t *testing.T) {
	traceTest(t)
	s, err := NewDenseMatrixStorage(5, 2048)
	if err != nil {
		t.Fatalf("new dense storage: %v", err)
	}

	h := s.HashForMatrix(0x1234)
	for r := 0; r < s.Rows(); r++ {
		col := int(h.RowHash(r, s.maskBits, s.mask))
		sign := float64(h.SignForRow(r))
		// Build signed values around 10,11,12,13,14.
		target := 10.0 + float64(r)
		s.UpdateOneCounter(r, col, target/sign)
	}

	med := s.FastQueryMedianSigned(h)
	if math.Abs(med-12) > 1e-9 {
		t.Fatalf("expected median 12, got %v", med)
	}
}
