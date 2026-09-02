// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package countsketch

import (
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// gosSampledSeed keeps the two samplers in a telescoping test making identical
// admission decisions (same seed + same call sequence ⇒ same subset).
const gosSampledSeed int64 = 0x5a3e06d

// residualPlusCrossings sums, per cell, a sketch's live residual plus every
// crossing ever reported for that cell — the value a receiver reconstructs by
// applying all deltas (+=) and then folding in the final residual frame. Under
// a correct detect+reset this must equal the total mass the cell ever
// accumulated.
func residualPlusCrossings(s *CountSketch, crossings []GOSCellUpdate) [][]float64 {
	m := make([][]float64, s.Rows)
	for r := 0; r < s.Rows; r++ {
		m[r] = make([]float64, s.Cols)
		copy(m[r], s.Count[r])
	}
	for _, d := range crossings {
		m[d.Row][d.Col] += d.Delta
	}
	return m
}

func assertMatrixEqual(t *testing.T, got, want *CountSketch, reconstructed [][]float64) {
	t.Helper()
	for r := 0; r < want.Rows; r++ {
		for c := 0; c < want.Cols; c++ {
			if math.Abs(reconstructed[r][c]-want.Count[r][c]) > 1e-9 {
				t.Fatalf("cell (%d,%d): telescoped=%v want=%v", r, c, reconstructed[r][c], want.Count[r][c])
			}
		}
	}
}

// TestUpdateStringAtRowsGOS_TelescopingExact is the core correctness proof for
// the row-admission GOS composition: with a FIXED admission bitmask and p, the
// GOS path's crossings + residual reconstruct, cell-for-cell, exactly what the
// non-GOS UpdateStringAtRows path accumulates. Row-admission is deterministic
// (no sampler), so this is an exact equality, isolating "does detect+reset
// telescope" from any sampling variance.
func TestUpdateStringAtRowsGOS_TelescopingExact(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 3000
	const admitted = uint64(0b10110) // rows 1,2,4 admit; 0,3 don't
	const p = 0.5
	const threshold = 6.0

	ref, _ := NewCountSketch(rows, cols)
	src, _ := NewCountSketch(rows, cols)
	var crossings []GOSCellUpdate
	for i := 0; i < n; i++ {
		ref.UpdateStringAtRows("k", 1.0, admitted, p)
		crossings = append(crossings, src.UpdateStringAtRowsGOS("k", 1.0, admitted, p, threshold)...)
	}
	assertMatrixEqual(t, src, ref, residualPlusCrossings(src, crossings))

	// Non-admitted rows must be entirely untouched in BOTH paths.
	for _, r := range []int{0, 3} {
		for c := 0; c < cols; c++ {
			if ref.Count[r][c] != 0 || src.Count[r][c] != 0 {
				t.Fatalf("non-admitted row %d col %d touched: ref=%v src=%v", r, c, ref.Count[r][c], src.Count[r][c])
			}
		}
	}
}

// TestUpdateStringSampledPerRowGOS_TelescopingExact proves the per-row
// geometric-admission GOS composition telescopes exactly. Two identically
// seeded samplers fed the same key sequence make identical per-row admission
// decisions, so the GOS path's crossings + residual reconstruct exactly what
// the non-GOS UpdateStringSampledPerRow path accumulates — sampling variance is
// present but IDENTICAL on both sides, so the equality is still exact.
func TestUpdateStringSampledPerRowGOS_TelescopingExact(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 4000
	const p = 0.4
	const threshold = 5.0

	ref, _ := NewCountSketch(rows, cols)
	src, _ := NewCountSketch(rows, cols)
	refSampler := common.NewGeometricSampler(p, gosSampledSeed)
	srcSampler := common.NewGeometricSampler(p, gosSampledSeed)

	var crossings []GOSCellUpdate
	for i := 0; i < n; i++ {
		ref.UpdateStringSampledPerRow("k", 1.0, refSampler)
		crossings = append(crossings, src.UpdateStringSampledPerRowGOS("k", 1.0, srcSampler, threshold)...)
	}
	assertMatrixEqual(t, src, ref, residualPlusCrossings(src, crossings))

	// The reconstructed frequency estimate must be within the sampling band of
	// the true count (n), confirming the composition didn't bias the estimate.
	rebuilt, _ := NewCountSketch(rows, cols)
	for r := 0; r < rows; r++ {
		copy(rebuilt.Count[r], residualPlusCrossings(src, crossings)[r])
	}
	est := float64(rebuilt.EstimateStringCount("k"))
	if est < 0.6*n || est > 1.4*n {
		t.Fatalf("reconstructed estimate %v outside sampling band around n=%d", est, n)
	}
}

// TestSampledGOS_DisabledFallsBack verifies threshold<=0 reports no crossings
// and leaves the sketch identical to the plain sampled path.
func TestSampledGOS_DisabledFallsBack(t *testing.T) {
	const rows, cols = 3, 256
	a, _ := NewCountSketch(rows, cols)
	b, _ := NewCountSketch(rows, cols)
	const admitted = uint64(0b101)
	for i := 0; i < 100; i++ {
		a.UpdateStringAtRows("k", 1.0, admitted, 0.5)
		if d := b.UpdateStringAtRowsGOS("k", 1.0, admitted, 0.5, 0); d != nil {
			t.Fatalf("threshold<=0 must report no crossings, got %v", d)
		}
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("disabled GOS diverged from plain at (%d,%d): %v vs %v", r, c, b.Count[r][c], a.Count[r][c])
			}
		}
	}
}

// TestSampledPerRowGOS_FullRateDegeneratesToGOS verifies a full-rate/nil
// sampler makes UpdateStringSampledPerRowGOS behave exactly like the
// un-sampled UpdateStringGOS (same crossings, same residual).
func TestSampledPerRowGOS_FullRateDegeneratesToGOS(t *testing.T) {
	const rows, cols = 4, 512
	const n = 500
	const threshold = 4.0
	a, _ := NewCountSketch(rows, cols)
	b, _ := NewCountSketch(rows, cols)
	var ca, cb []GOSCellUpdate
	for i := 0; i < n; i++ {
		ca = append(ca, a.UpdateStringGOS("k", 1.0, threshold)...)
		cb = append(cb, b.UpdateStringSampledPerRowGOS("k", 1.0, nil, threshold)...)
	}
	if len(ca) != len(cb) {
		t.Fatalf("crossing counts differ: UpdateStringGOS=%d nil-sampler=%d", len(ca), len(cb))
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if a.Count[r][c] != b.Count[r][c] {
				t.Fatalf("residual diverged at (%d,%d): %v vs %v", r, c, a.Count[r][c], b.Count[r][c])
			}
		}
	}
}
