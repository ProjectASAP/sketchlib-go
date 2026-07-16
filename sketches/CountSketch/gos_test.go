// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package countsketch

import (
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// TestUpdateStringGOS_Disabled verifies threshold<=0 behaves exactly like
// UpdateString (no cells reported, normal accumulation, no reset).
func TestUpdateStringGOS_Disabled(t *testing.T) {
	s, err := NewCountSketch(3, 64)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	for i := 0; i < 10; i++ {
		if dirty := s.UpdateStringGOS("k", 1.0, 0); dirty != nil {
			t.Fatalf("threshold<=0 must report no dirty cells, got %v", dirty)
		}
	}
	if got := s.EstimateStringCount("k"); got != 10 {
		t.Fatalf("EstimateStringCount: got %d, want 10", got)
	}
}

// TestUpdateStringGOS_CrossesAndResets drives a single-row sketch (so the
// touched column is deterministic across inserts) until the cell crosses a
// fixed threshold, and verifies: the crossing is reported with the correct
// magnitude, the cell is reset to 0 in place, and L2 reflects the reset (a
// single-row, otherwise-untouched-column sketch has no other contribution).
func TestUpdateStringGOS_CrossesAndResets(t *testing.T) {
	s, err := NewCountSketch(1, 2)
	if err != nil {
		t.Fatalf("NewCountSketch: %v", err)
	}
	const threshold = 3.0
	input := common.FromString("k")
	col := s.ColForRow(input, 0)
	sign := s.SignForRow(input, 0)

	var crossed []GOSCellUpdate
	var i int
	for i = 0; i < 100; i++ {
		dirty := s.UpdateStringGOS("k", 1.0, threshold)
		if len(dirty) > 0 {
			crossed = dirty
			break
		}
	}
	if len(crossed) != 1 {
		t.Fatalf("expected exactly one crossing, got %d after %d inserts", len(crossed), i+1)
	}
	got := crossed[0]
	if got.Row != 0 || got.Col != uint32(col) {
		t.Fatalf("GOSCellUpdate{Row,Col} = {%d,%d}, want {0,%d}", got.Row, got.Col, col)
	}
	wantDelta := sign * float64(i+1)
	if math.Abs(got.Delta-wantDelta) > 1e-9 {
		t.Fatalf("GOSCellUpdate.Delta = %v, want %v (sign %v after %d inserts)", got.Delta, wantDelta, sign, i+1)
	}
	if math.Abs(got.Delta) < threshold {
		t.Fatalf("reported crossing %v did not actually reach threshold %v", got.Delta, threshold)
	}
	// The cell must be zeroed in place immediately.
	if cell := s.GetCell(0, col); cell != 0 {
		t.Fatalf("GetCell after crossing: got %v, want 0", cell)
	}
	// Single-row, single-touched-column sketch: L2 must also be exactly 0
	// after the reset (no other cell contributes).
	if s.L2[0] != 0 {
		t.Fatalf("L2[0] after crossing+reset: got %v, want 0", s.L2[0])
	}
}

// TestUpdateStringGOS_TelescopingReconstruction is the key correctness
// property this mechanism depends on: even though the SOURCE sketch's cells
// are repeatedly zeroed in place as they cross threshold, applying every
// GOSCellUpdate ever reported onto a fresh TARGET sketch (as a += delta,
// mirroring delta.ApplyDelta's cell semantics) reconstructs the same
// estimate a never-reset sketch fed the identical inserts would answer.
func TestUpdateStringGOS_TelescopingReconstruction(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 2000
	source, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	reference, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	target, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	const threshold = 4.0
	for i := 0; i < n; i++ {
		dirty := source.UpdateStringGOS("k", 1.0, threshold)
		// Apply each crossing onto target with += semantics (mirrors
		// delta.ApplyDelta's cell handling) — this test only reads
		// EstimateStringCount (the Count matrix), so L2 bookkeeping on
		// target is irrelevant here.
		for _, d := range dirty {
			target.Count[d.Row][d.Col] += d.Delta
		}
		reference.UpdateString("k", 1.0)
	}
	// Drain whatever's left un-crossed in source directly (mirrors what a
	// window-boundary flush would do: the residual below threshold is simply
	// what the mechanism has not yet sent — apply it too so this test
	// isolates "does telescoping reconstruct the true value" from "residual
	// below threshold is accepted lossy compression", which is a separate,
	// already-accepted property).
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if v := source.Count[r][c]; v != 0 {
				target.Count[r][c] += v
			}
		}
	}

	got := target.EstimateStringCount("k")
	want := reference.EstimateStringCount("k")
	if got != want {
		t.Fatalf("telescoped estimate = %d, want %d (reference, never reset)", got, want)
	}
}
