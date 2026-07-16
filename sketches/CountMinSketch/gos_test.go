// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package countminsketch

import (
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// TestInsertWithHashGOS_Disabled verifies threshold<=0 behaves exactly like
// FastInsertWeightWithHashValue (no cells reported, normal accumulation, no
// reset).
func TestInsertWithHashGOS_Disabled(t *testing.T) {
	s, err := NewCountMinSketch(3, 64)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	h := common.FromString("k").Hash
	for i := 0; i < 10; i++ {
		if dirty := s.InsertWithHashGOS(h, 1.0, 0); dirty != nil {
			t.Fatalf("threshold<=0 must report no dirty cells, got %v", dirty)
		}
	}
	if got := s.FastEstimateWithHash(h); got != 10 {
		t.Fatalf("FastEstimateWithHash: got %v, want 10", got)
	}
}

// TestInsertWithHashGOS_CrossesAndResets drives a single-row sketch (so the
// touched column is deterministic across inserts) until the cell crosses a
// fixed threshold, and verifies: the crossing is reported with the correct
// (non-negative) magnitude, the cell is reset to 0 in place, and L1/L2
// reflect the reset (a single-row, single-key sketch has no other
// contribution).
func TestInsertWithHashGOS_CrossesAndResets(t *testing.T) {
	s, err := NewCountMinSketch(1, 64)
	if err != nil {
		t.Fatalf("NewCountMinSketch: %v", err)
	}
	const threshold = 3.0
	h := common.FromString("k").Hash
	col := s.ColForRow(&common.SketchInput{Hash: h}, 0)

	var crossed []GOSCellUpdate
	var i int
	for i = 0; i < 100; i++ {
		dirty := s.InsertWithHashGOS(h, 1.0, threshold)
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
	wantDelta := float64(i + 1)
	if math.Abs(got.Delta-wantDelta) > 1e-9 {
		t.Fatalf("GOSCellUpdate.Delta = %v, want %v (after %d inserts)", got.Delta, wantDelta, i+1)
	}
	if got.Delta < threshold {
		t.Fatalf("reported crossing %v did not actually reach threshold %v", got.Delta, threshold)
	}
	// The cell must be zeroed in place immediately.
	if cell := s.GetCell(0, col); cell != 0 {
		t.Fatalf("GetCell after crossing: got %v, want 0", cell)
	}
	// Single-row, single-key sketch: L1/L2 must also be exactly 0 after the
	// reset (no other cell contributes).
	if s.L1[0] != 0 {
		t.Fatalf("L1[0] after crossing+reset: got %v, want 0", s.L1[0])
	}
	if s.L2[0] != 0 {
		t.Fatalf("L2[0] after crossing+reset: got %v, want 0", s.L2[0])
	}
}

// TestInsertWithHashGOS_TelescopingReconstruction is the key correctness
// property this mechanism depends on: even though the SOURCE sketch's cells
// are repeatedly zeroed in place as they cross threshold, applying every
// GOSCellUpdate ever reported onto a fresh TARGET sketch (as a += delta,
// mirroring ApplyDelta's cell semantics) reconstructs the same estimate a
// never-reset sketch fed the identical inserts would answer.
func TestInsertWithHashGOS_TelescopingReconstruction(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 2000
	source, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	reference, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	target, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	h := common.FromString("k").Hash
	const threshold = 4.0
	for i := 0; i < n; i++ {
		dirty := source.InsertWithHashGOS(h, 1.0, threshold)
		// Apply each crossing onto target with += semantics (mirrors
		// ApplyDelta's cell handling) — this test only reads
		// FastEstimateWithHash (the Count matrix), so Sum/Sum2 bookkeeping on
		// target is irrelevant here.
		for _, d := range dirty {
			target.Count[d.Row][d.Col] += d.Delta
		}
		reference.InsertWithHash(h)
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

	got := target.FastEstimateWithHash(h)
	want := reference.FastEstimateWithHash(h)
	if got != want {
		t.Fatalf("telescoped estimate = %v, want %v (reference, never reset)", got, want)
	}
}
