package foldcountminsketch

import (
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	foldcommon "github.com/ProjectASAP/sketchlib-go/sketches/FoldCommon"
)

func TestFoldCellLifecycle(t *testing.T) {
	var cell foldcommon.FoldCell
	if !cell.IsEmpty() {
		t.Fatalf("zero-value cell must be empty")
	}

	cell.Insert(7, 3)
	cell.Insert(7, 2)
	if got := cell.Query(7); got != 5 {
		t.Fatalf("same-key accumulation mismatch: got %v", got)
	}
	if got := cell.EntryCount(); got != 1 {
		t.Fatalf("expected single entry, got %d", got)
	}

	cell.Insert(9, 4)
	if got := cell.Query(9); got != 4 {
		t.Fatalf("collision entry mismatch: got %v", got)
	}
	if got := cell.EntryCount(); got != 2 {
		t.Fatalf("expected collision fanout, got %d", got)
	}
}

func TestFoldCountMinMatchesDenseSketch(t *testing.T) {
	rows, cols, foldLevel := 3, 512, uint32(4)
	folded, err := NewFoldCountMinSketch(rows, cols, foldLevel)
	if err != nil {
		t.Fatalf("new folded count-min: %v", err)
	}
	dense, err := countminsketch.NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("new dense count-min: %v", err)
	}

	for i := 0; i < 200; i++ {
		input := common.FromU64(uint64(i))
		weight := float64((i % 7) + 1)
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

func TestFoldCountMinMergeSameLevelMatchesDense(t *testing.T) {
	rows, cols, foldLevel := 3, 256, uint32(3)
	left, _ := NewFoldCountMinSketch(rows, cols, foldLevel)
	right, _ := NewFoldCountMinSketch(rows, cols, foldLevel)
	denseLeft, _ := countminsketch.NewCountMinSketch(rows, cols)
	denseRight, _ := countminsketch.NewCountMinSketch(rows, cols)

	for i := 0; i < 50; i++ {
		input := common.FromU64(uint64(i))
		left.InsertWeight(input, float64(i+1))
		denseLeft.InsertWeight(input, float64(i+1))
	}
	for i := 25; i < 75; i++ {
		input := common.FromU64(uint64(i))
		right.InsertWeight(input, float64(i+1))
		denseRight.InsertWeight(input, float64(i+1))
	}

	if err := left.MergeSameLevel(right); err != nil {
		t.Fatalf("merge same level: %v", err)
	}
	if err := denseLeft.Merge(denseRight); err != nil {
		t.Fatalf("dense merge: %v", err)
	}

	for i := 0; i < 75; i++ {
		input := common.FromU64(uint64(i))
		if got, want := left.Estimate(input), denseLeft.Estimate(input); got != want {
			t.Fatalf("post-merge estimate mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountMinUnfoldAndHierarchicalMerge(t *testing.T) {
	rows, cols := 3, 1024
	a, _ := NewFoldCountMinSketch(rows, cols, 4)
	b, _ := NewFoldCountMinSketch(rows, cols, 2)
	dense, _ := countminsketch.NewCountMinSketch(rows, cols)

	for i := 0; i < 100; i++ {
		input := common.FromU64(uint64(i))
		a.InsertWeight(input, 1)
		dense.InsertWeight(input, 1)
	}
	for i := 50; i < 150; i++ {
		input := common.FromU64(uint64(i))
		b.InsertWeight(input, 2)
		dense.InsertWeight(input, 2)
	}

	unfolded, err := a.UnfoldTo(0)
	if err != nil {
		t.Fatalf("unfold to full: %v", err)
	}
	if unfolded.FoldLevel != 0 || unfolded.FoldCols != cols {
		t.Fatalf("unexpected unfolded geometry: level=%d cols=%d", unfolded.FoldLevel, unfolded.FoldCols)
	}

	merged, err := HierarchicalMergeFoldCountMinSketch([]*FoldCountMinSketch{a, b})
	if err != nil {
		t.Fatalf("hierarchical merge: %v", err)
	}
	if merged.FoldLevel != 0 {
		t.Fatalf("hierarchical merge must end at level 0, got %d", merged.FoldLevel)
	}

	for i := 0; i < 150; i++ {
		input := common.FromU64(uint64(i))
		if got, want := merged.Estimate(input), dense.Estimate(input); got != want {
			t.Fatalf("hierarchical merge mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountMinSerializationRoundTrip(t *testing.T) {
	sketch, _ := NewFoldCountMinSketch(3, 256, 3)
	for i := 0; i < 40; i++ {
		sketch.InsertWeight(common.FromU64(uint64(i)), float64(i+1))
	}

	data, err := sketch.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	decoded, err := DeserializeFoldCountMinSketchFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	for i := 0; i < 40; i++ {
		got := decoded.Estimate(common.FromU64(uint64(i)))
		want := sketch.Estimate(common.FromU64(uint64(i)))
		if got != want {
			t.Fatalf("round-trip mismatch for key %d: got %v want %v", i, got, want)
		}
	}
}

func TestFoldCountMinSparseCollisionProfile(t *testing.T) {
	sketch, _ := NewFoldCountMinSketch(3, 4096, 4)
	for i := 0; i < 60; i++ {
		sketch.Insert(common.FromU64(uint64(i)))
	}
	if sketch.TotalEntries() < 150 {
		t.Fatalf("unexpectedly low entry count: %d", sketch.TotalEntries())
	}
	if sketch.CollidedCells() > 40 {
		t.Fatalf("too many sparse collisions: %d", sketch.CollidedCells())
	}
	if math.IsNaN(sketch.L2[0]) {
		t.Fatalf("invalid norm state")
	}
}
