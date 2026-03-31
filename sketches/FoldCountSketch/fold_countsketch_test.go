package foldcountsketch

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
)

func TestFoldCountSketchMatchesDenseSketch(t *testing.T) {
	rows, cols, foldLevel := 3, 512, uint32(4)
	folded, err := NewFoldCountSketch(rows, cols, foldLevel)
	if err != nil {
		t.Fatalf("new folded count-sketch: %v", err)
	}
	dense, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("new dense count-sketch: %v", err)
	}

	for i := 0; i < 200; i++ {
		input := common.FromU64(uint64(i))
		weight := float64((i % 9) + 1)
		folded.InsertWeight(input, weight)
		dense.InsertWeight(input, weight)
	}

	for i := 0; i < 200; i++ {
		input := common.FromU64(uint64(i))
		got := folded.Estimate(input)
		want := dense.Estimate(input)
		if got != want {
			t.Fatalf("estimate mismatch for key %d: got %v want %v", i, got, want)
		}
	}

	flat := folded.ToFlatCounters()
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			got := flat[row*cols+col]
			want := dense.Count[row][col]
			if got != want {
				t.Fatalf("flat counter mismatch at row=%d col=%d: got %v want %v", row, col, got, want)
			}
		}
	}
}

func TestFoldCountSketchMergeAndHierarchicalMerge(t *testing.T) {
	rows, cols := 3, 1024
	left, _ := NewFoldCountSketch(rows, cols, 3)
	right, _ := NewFoldCountSketch(rows, cols, 3)
	lowFold, _ := NewFoldCountSketch(rows, cols, 1)
	dense, _ := countsketch.NewCountSketch(rows, cols)

	for i := 0; i < 80; i++ {
		input := common.FromU64(uint64(i))
		left.InsertWeight(input, 1)
		dense.InsertWeight(input, 1)
	}
	for i := 40; i < 120; i++ {
		input := common.FromU64(uint64(i))
		right.InsertWeight(input, 2)
		dense.InsertWeight(input, 2)
	}
	for i := 100; i < 160; i++ {
		input := common.FromU64(uint64(i))
		lowFold.InsertWeight(input, 3)
		dense.InsertWeight(input, 3)
	}

	if err := left.MergeSameLevel(right); err != nil {
		t.Fatalf("merge same level: %v", err)
	}
	for i := 0; i < 120; i++ {
		input := common.FromU64(uint64(i))
		checkDense, _ := countsketch.NewCountSketch(rows, cols)
		_ = checkDense
		_ = input
	}

	merged, err := HierarchicalMergeFoldCountSketch([]*FoldCountSketch{left, lowFold})
	if err != nil {
		t.Fatalf("hierarchical merge: %v", err)
	}
	if merged.FoldLevel != 0 {
		t.Fatalf("hierarchical merge must end at level 0, got %d", merged.FoldLevel)
	}

	for i := 0; i < 160; i++ {
		input := common.FromU64(uint64(i))
		if got, want := merged.Estimate(input), dense.Estimate(input); got != want {
			t.Fatalf("hierarchical merge mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountSketchUnfoldMergeMatchesDense(t *testing.T) {
	rows, cols, foldLevel := 3, 256, uint32(2)
	a, _ := NewFoldCountSketch(rows, cols, foldLevel)
	b, _ := NewFoldCountSketch(rows, cols, foldLevel)
	denseA, _ := countsketch.NewCountSketch(rows, cols)
	denseB, _ := countsketch.NewCountSketch(rows, cols)

	for i := 0; i < 60; i++ {
		input := common.FromU64(uint64(i))
		a.InsertWeight(input, float64(i+1))
		denseA.InsertWeight(input, float64(i+1))
	}
	for i := 30; i < 90; i++ {
		input := common.FromU64(uint64(i))
		b.InsertWeight(input, float64(i+1))
		denseB.InsertWeight(input, float64(i+1))
	}

	merged, err := UnfoldMergeFoldCountSketch(a, b)
	if err != nil {
		t.Fatalf("unfold merge: %v", err)
	}
	if err := denseA.Merge(denseB); err != nil {
		t.Fatalf("dense merge: %v", err)
	}

	for i := 0; i < 90; i++ {
		input := common.FromU64(uint64(i))
		if got, want := merged.Estimate(input), denseA.Estimate(input); got != want {
			t.Fatalf("unfold merge mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountSketchSerializationRoundTrip(t *testing.T) {
	sketch, _ := NewFoldCountSketch(3, 256, 3)
	for i := 0; i < 50; i++ {
		sketch.InsertWeight(common.FromU64(uint64(i)), float64(i+1))
	}

	data, err := sketch.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	decoded, err := DeserializeFoldCountSketchFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	for i := 0; i < 50; i++ {
		input := common.FromU64(uint64(i))
		if got, want := decoded.Estimate(input), sketch.Estimate(input); got != want {
			t.Fatalf("round-trip mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountSketchSparseCollisionProfile(t *testing.T) {
	sketch, _ := NewFoldCountSketch(3, 4096, 4)
	hasPos := false
	hasNeg := false
	for i := 0; i < 60; i++ {
		sketch.Insert(common.FromU64(uint64(i)))
	}
	for i := range sketch.Cells {
		sketch.Cells[i].Visit(func(_ uint32, count float64) {
			if count > 0 {
				hasPos = true
			}
			if count < 0 {
				hasNeg = true
			}
		})
	}
	if !hasPos || !hasNeg {
		t.Fatalf("expected both positive and negative signed entries")
	}
	if sketch.CollidedCells() > 40 {
		t.Fatalf("too many sparse collisions: %d", sketch.CollidedCells())
	}
}
