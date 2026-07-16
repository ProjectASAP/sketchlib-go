package countminsketch

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// InsertWithHashAtRows, given the SAME admission decisions a
// GeometricSampler would have made, must produce a BYTE-IDENTICAL matrix to
// InsertWithHashSampledPerRow.
func TestInsertWithHashAtRows_MatchesSampledPerRowForSameDecisions(t *testing.T) {
	const (
		rows, cols = 4, 1024
		n          = 2000
		p          = 0.4
	)
	viaSampler, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	viaBitmask, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}

	sDriver := common.NewGeometricSampler(p, 999)
	sCapture := common.NewGeometricSampler(p, 999)

	for i := 0; i < n; i++ {
		hash := common.Hash64([]byte{byte(i % 200)})

		viaSampler.InsertWithHashSampledPerRow(hash, sDriver)

		sCapture.BeginItem()
		var mask uint64
		for r := 0; r < rows; r++ {
			if sCapture.Admit() {
				mask |= 1 << uint(r)
			}
		}
		viaBitmask.InsertWithHashAtRows(hash, 1.0, mask, p)
	}

	for r := 0; r < rows; r++ {
		cs := viaSampler.countStore.RowSlice(r)
		cb := viaBitmask.countStore.RowSlice(r)
		for c := 0; c < cols; c++ {
			if cs[c] != cb[c] {
				t.Fatalf("cell (%d,%d) diverged: sampler-driven=%v bitmask-driven=%v", r, c, cs[c], cb[c])
			}
		}
	}
}

// A zero bitmask (R(x)=∅) must be a true no-op.
func TestInsertWithHashAtRows_ZeroMaskIsNoop(t *testing.T) {
	cms, _ := NewCountMinSketch(4, 256)
	hash := common.Hash64([]byte("x"))
	cms.InsertWithHashAtRows(hash, 3.0, 0, 0.3)
	if got := cms.FastEstimateWithHash(hash); got != 0 {
		t.Fatalf("zero admittedRows must not touch the sketch, got estimate %v", got)
	}
}

// p>=1 (no rescale) with all rows admitted must equal the plain InsertWithHash.
func TestInsertWithHashAtRows_FullRateAllRowsEqualsPlain(t *testing.T) {
	a, _ := NewCountMinSketch(4, 1024)
	b, _ := NewCountMinSketch(4, 1024)
	allRows := uint64(0b1111)
	for i := 0; i < 1000; i++ {
		hash := common.Hash64([]byte{byte(i % 50)})
		a.InsertWithHashAtRows(hash, 1.0, allRows, 1.0)
		b.InsertWithHash(hash)
	}
	for r := 0; r < a.Rows; r++ {
		ca := a.countStore.RowSlice(r)
		cb := b.countStore.RowSlice(r)
		for c := 0; c < a.Cols; c++ {
			if ca[c] != cb[c] {
				t.Fatalf("p=1, all rows admitted must equal plain InsertWithHash at (%d,%d): %v vs %v", r, c, ca[c], cb[c])
			}
		}
	}
}
