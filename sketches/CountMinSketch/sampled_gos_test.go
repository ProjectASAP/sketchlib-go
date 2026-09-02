// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package countminsketch

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

const gosSampledSeedCMS int64 = 0x5a3e06d

func NewCountMinSketchMust(rows, cols int) *CountMinSketch {
	s, err := NewCountMinSketch(rows, cols)
	if err != nil {
		panic(err)
	}
	return s
}

// cmsResidualPlusCrossings sums, per cell, the sketch's live COUNT residual
// plus every reported crossing for that cell — the value a receiver
// reconstructs by applying all deltas (+=) then folding in the final residual.
func cmsResidualPlusCrossings(s *CountMinSketch, crossings []GOSCellUpdate) [][]float64 {
	m := make([][]float64, s.Rows)
	for r := 0; r < s.Rows; r++ {
		m[r] = make([]float64, s.Cols)
		copy(m[r], s.countStore.RowSlice(r))
	}
	for _, d := range crossings {
		m[d.Row][d.Col] += d.Delta
	}
	return m
}

func cmsAssertCountEqual(t *testing.T, want *CountMinSketch, reconstructed [][]float64) {
	t.Helper()
	for r := 0; r < want.Rows; r++ {
		wantRow := want.countStore.RowSlice(r)
		for c := 0; c < want.Cols; c++ {
			if diff := reconstructed[r][c] - wantRow[c]; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("count cell (%d,%d): telescoped=%v want=%v", r, c, reconstructed[r][c], wantRow[c])
			}
		}
	}
}

// TestCMS_InsertWithHashAtRowsGOS_TelescopingExact — fixed bitmask + p, so the
// GOS path's crossings + residual reconstruct the non-GOS InsertWithHashAtRows
// count matrix exactly (row-admission is deterministic, no sampler variance).
func TestCMS_InsertWithHashAtRowsGOS_TelescopingExact(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 3000
	const admitted = uint64(0b10110)
	const p = 0.5
	const threshold = 6.0
	const hash = uint64(0x1234_5678_9abc_def0)

	ref := NewCountMinSketchMust(rows, cols)
	src := NewCountMinSketchMust(rows, cols)
	var crossings []GOSCellUpdate
	for i := 0; i < n; i++ {
		ref.InsertWithHashAtRows(hash, 1.0, admitted, p)
		crossings = append(crossings, src.InsertWithHashAtRowsGOS(hash, 1.0, admitted, p, threshold)...)
	}
	cmsAssertCountEqual(t, ref, cmsResidualPlusCrossings(src, crossings))

	for _, r := range []int{0, 3} {
		row := src.countStore.RowSlice(r)
		for c := 0; c < cols; c++ {
			if row[c] != 0 {
				t.Fatalf("non-admitted row %d col %d touched: %v", r, c, row[c])
			}
		}
	}
}

// TestCMS_InsertWithHashSampledPerRowGOS_TelescopingExact — two identically
// seeded samplers make identical admission decisions, so the GOS path
// reconstructs the non-GOS count matrix exactly.
func TestCMS_InsertWithHashSampledPerRowGOS_TelescopingExact(t *testing.T) {
	const rows, cols = 5, 1024
	const n = 4000
	const p = 0.4
	const threshold = 5.0
	const hash = uint64(0x0fed_cba9_8765_4321)

	ref := NewCountMinSketchMust(rows, cols)
	src := NewCountMinSketchMust(rows, cols)
	refSampler := common.NewGeometricSampler(p, gosSampledSeedCMS)
	srcSampler := common.NewGeometricSampler(p, gosSampledSeedCMS)

	var crossings []GOSCellUpdate
	for i := 0; i < n; i++ {
		ref.InsertWithHashSampledPerRow(hash, refSampler)
		crossings = append(crossings, src.InsertWithHashSampledPerRowGOS(hash, srcSampler, threshold)...)
	}
	cmsAssertCountEqual(t, ref, cmsResidualPlusCrossings(src, crossings))
}

// TestCMS_SampledGOS_DisabledFallsBack — threshold<=0 reports nothing and
// leaves the count matrix identical to the plain sampled path.
func TestCMS_SampledGOS_DisabledFallsBack(t *testing.T) {
	const rows, cols = 3, 256
	const hash = uint64(0xdead_beef_cafe_f00d)
	a := NewCountMinSketchMust(rows, cols)
	b := NewCountMinSketchMust(rows, cols)
	const admitted = uint64(0b101)
	for i := 0; i < 100; i++ {
		a.InsertWithHashAtRows(hash, 1.0, admitted, 0.5)
		if d := b.InsertWithHashAtRowsGOS(hash, 1.0, admitted, 0.5, 0); d != nil {
			t.Fatalf("threshold<=0 must report no crossings, got %v", d)
		}
	}
	for r := 0; r < rows; r++ {
		ar, br := a.countStore.RowSlice(r), b.countStore.RowSlice(r)
		for c := 0; c < cols; c++ {
			if ar[c] != br[c] {
				t.Fatalf("disabled GOS diverged at (%d,%d): %v vs %v", r, c, br[c], ar[c])
			}
		}
	}
}
