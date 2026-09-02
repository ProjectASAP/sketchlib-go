package countsketch

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// UpdateStringAtRows, given the SAME admission decisions a GeometricSampler
// would have made, must produce a BYTE-IDENTICAL matrix to
// UpdateStringSampledPerRow — the whole point of the new method is to apply
// a PRE-DECIDED bitmask via the identical hash/sign/cell-update logic, not
// a different one.
func TestUpdateStringAtRows_MatchesSampledPerRowForSameDecisions(t *testing.T) {
	const (
		rows, cols = 5, 1024
		n          = 2000
		p          = 0.4
	)
	viaSampler, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	viaBitmask, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}

	// Two samplers seeded IDENTICALLY: one drives UpdateStringSampledPerRow
	// directly; the other's decisions are captured by hand into a bitmask,
	// which drives UpdateStringAtRows on the second sketch.
	sDriver := common.NewGeometricSampler(p, 999)
	sCapture := common.NewGeometricSampler(p, 999)

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%d", i%200)

		viaSampler.UpdateStringSampledPerRow(key, 1.0, sDriver)

		sCapture.BeginItem()
		var mask uint64
		for r := 0; r < rows; r++ {
			if sCapture.Admit() {
				mask |= 1 << uint(r)
			}
		}
		viaBitmask.UpdateStringAtRows(key, 1.0, mask, p)
	}

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if viaSampler.Count[r][c] != viaBitmask.Count[r][c] {
				t.Fatalf("cell (%d,%d) diverged: sampler-driven=%v bitmask-driven=%v",
					r, c, viaSampler.Count[r][c], viaBitmask.Count[r][c])
			}
		}
	}
}

// A zero bitmask (R(x)=∅) must be a true no-op.
func TestUpdateStringAtRows_ZeroMaskIsNoop(t *testing.T) {
	cs, _ := NewCountSketch(5, 256)
	cs.UpdateStringAtRows("x", 3.0, 0, 0.3)
	if got := cs.EstimateStringCount("x"); got != 0 {
		t.Fatalf("zero admittedRows must not touch the sketch, got estimate %v", got)
	}
}

// p>=1 (no rescale) with all rows admitted must equal the plain UpdateString.
func TestUpdateStringAtRows_FullRateAllRowsEqualsPlain(t *testing.T) {
	a, _ := NewCountSketch(5, 1024)
	b, _ := NewCountSketch(5, 1024)
	allRows := uint64(0b11111)
	for i := 0; i < 1000; i++ {
		k := fmt.Sprintf("k%d", i%50)
		a.UpdateStringAtRows(k, 1.0, allRows, 1.0)
		b.UpdateString(k, 1.0)
	}
	for r := 0; r < a.Rows; r++ {
		for c := 0; c < a.Cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("p=1, all rows admitted must equal plain UpdateString at (%d,%d): %v vs %v", r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}
