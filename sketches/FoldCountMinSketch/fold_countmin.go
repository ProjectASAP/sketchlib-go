package foldcountminsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	foldcommon "github.com/ProjectASAP/sketchlib-go/sketches/FoldCommon"
)

type FoldCountMinSketch struct {
	Rows      int
	FoldCols  int
	FullCols  int
	FoldLevel uint32
	Cells     []foldcommon.FoldCell
	L1        []float64
	L2        []float64

	bitsPerRow uint
	mask       uint64
}

func NewFoldCountMinSketch(rows, fullCols int, foldLevel uint32) (*FoldCountMinSketch, error) {
	if rows <= 0 || fullCols <= 0 {
		return nil, errors.New("rows and fullCols must be positive")
	}
	if fullCols&(fullCols-1) != 0 {
		return nil, errors.New("fullCols must be a power of two")
	}
	maxFoldLevel := uint32(bits.TrailingZeros(uint(fullCols)))
	if foldLevel > maxFoldLevel {
		return nil, errors.New("foldLevel too large for fullCols")
	}

	s := &FoldCountMinSketch{
		Rows:      rows,
		FoldCols:  fullCols >> foldLevel,
		FullCols:  fullCols,
		FoldLevel: foldLevel,
		Cells:     make([]foldcommon.FoldCell, rows*(fullCols>>foldLevel)),
		L1:        make([]float64, rows),
		L2:        make([]float64, rows),
	}
	s.bitsPerRow = uint(bits.TrailingZeros(uint(fullCols)))
	s.mask = uint64(fullCols - 1)
	return s, nil
}

func NewFoldCountMinSketchFull(rows, fullCols int) (*FoldCountMinSketch, error) {
	return NewFoldCountMinSketch(rows, fullCols, 0)
}

func (s *FoldCountMinSketch) rehydrate() error {
	if s.Rows <= 0 || s.FullCols <= 0 || s.FoldCols <= 0 {
		return errors.New("invalid folded count-min dimensions")
	}
	if s.FullCols&(s.FullCols-1) != 0 {
		return errors.New("invalid folded count-min fullCols")
	}
	if s.FoldCols != s.FullCols>>s.FoldLevel {
		return errors.New("invalid folded count-min foldCols")
	}
	if len(s.Cells) != s.Rows*s.FoldCols {
		return errors.New("invalid folded count-min cell count")
	}
	if len(s.L1) != s.Rows || len(s.L2) != s.Rows {
		return errors.New("invalid folded count-min norm sizes")
	}
	s.bitsPerRow = uint(bits.TrailingZeros(uint(s.FullCols)))
	s.mask = uint64(s.FullCols - 1)
	return nil
}

func (s *FoldCountMinSketch) TypeName() string { return "fold_countmin" }

func (s *FoldCountMinSketch) RowCount() int  { return s.Rows }
func (s *FoldCountMinSketch) ColCount() int  { return s.FullCols }
func (s *FoldCountMinSketch) CellCount() int { return len(s.Cells) }

func (s *FoldCountMinSketch) TotalEntries() int {
	total := 0
	for i := range s.Cells {
		total += s.Cells[i].EntryCount()
	}
	return total
}

func (s *FoldCountMinSketch) CollidedCells() int {
	total := 0
	for i := range s.Cells {
		if s.Cells[i].EntryCount() > 1 {
			total++
		}
	}
	return total
}

func (s *FoldCountMinSketch) InsertWithHash(hash uint64) {
	s.insertHashed(storage.BuildMatrixHash(hash, s.Rows, s.FullCols), 1)
}

// Update is the canonical high-level update method, mirroring Rust's
// unified API (`update`). It populates the sketch from a SketchInput.
func (s *FoldCountMinSketch) Update(input *common.SketchInput) {
	if input == nil {
		return
	}
	s.insertHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols), 1)
}

// OctoUpdate is an alias for Update kept for the OctoSketch framework.
func (s *FoldCountMinSketch) OctoUpdate(input *common.SketchInput) { s.Update(input) }

func (s *FoldCountMinSketch) UpdateWeight(input *common.SketchInput, many float64) {
	if input == nil || many == 0 {
		return
	}
	s.insertHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols), many)
}

func (s *FoldCountMinSketch) insertHashed(hashed storage.MatrixHashType, many float64) {
	for row := 0; row < s.Rows; row++ {
		fullCol := uint32(hashed.RowHash(row, s.bitsPerRow, s.mask))
		cellIdx := row*s.FoldCols + s.foldColOf(fullCol)
		prev := s.Cells[cellIdx].Query(fullCol)
		s.Cells[cellIdx].Insert(fullCol, many)
		curr := prev + many
		s.L1[row] += many
		s.L2[row] += curr*curr - prev*prev
	}
}

func (s *FoldCountMinSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q != common.QueryFrequency {
		return 0, common.ErrUnsupportedQuery
	}
	return s.queryHashed(storage.BuildMatrixHash(hash, s.Rows, s.FullCols)), nil
}

func (s *FoldCountMinSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	return s.queryHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols))
}

// OctoEstimate satisfies the octosketch.OctoSketch interface.
func (s *FoldCountMinSketch) OctoEstimate(input *common.SketchInput) float64 {
	return s.Estimate(input)
}

func (s *FoldCountMinSketch) queryHashed(hashed storage.MatrixHashType) float64 {
	res := math.MaxFloat64
	for row := 0; row < s.Rows; row++ {
		fullCol := uint32(hashed.RowHash(row, s.bitsPerRow, s.mask))
		value := s.Cells[row*s.FoldCols+s.foldColOf(fullCol)].Query(fullCol)
		if value < res {
			res = value
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return res
}

func (s *FoldCountMinSketch) Merge(other common.Sketch) error {
	o, ok := other.(*FoldCountMinSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	return s.MergeSameLevel(o)
}

func (s *FoldCountMinSketch) MergeSameLevel(other *FoldCountMinSketch) error {
	if other == nil {
		return errors.New("cannot merge: nil other")
	}
	if s.Rows != other.Rows || s.FullCols != other.FullCols || s.FoldLevel != other.FoldLevel || s.FoldCols != other.FoldCols {
		return errors.New("cannot merge: folded dimension mismatch")
	}
	for i := range s.Cells {
		s.Cells[i].MergeFrom(&other.Cells[i])
	}
	s.recomputeNorms()
	return nil
}

func (s *FoldCountMinSketch) UnfoldTo(targetLevel uint32) (*FoldCountMinSketch, error) {
	if targetLevel > s.FoldLevel {
		return nil, errors.New("target level must be <= current fold level")
	}
	if targetLevel == s.FoldLevel {
		clone := *s
		clone.Cells = append([]foldcommon.FoldCell(nil), s.Cells...)
		clone.L1 = append([]float64(nil), s.L1...)
		clone.L2 = append([]float64(nil), s.L2...)
		return &clone, nil
	}
	target, err := NewFoldCountMinSketch(s.Rows, s.FullCols, targetLevel)
	if err != nil {
		return nil, err
	}
	s.scatterInto(target)
	target.recomputeNorms()
	return target, nil
}

func (s *FoldCountMinSketch) UnfoldFull() (*FoldCountMinSketch, error) {
	return s.UnfoldTo(0)
}

func UnfoldMergeFoldCountMinSketch(a, b *FoldCountMinSketch) (*FoldCountMinSketch, error) {
	if a == nil || b == nil {
		return nil, errors.New("cannot unfold merge nil sketch")
	}
	if a.Rows != b.Rows || a.FullCols != b.FullCols || a.FoldLevel != b.FoldLevel {
		return nil, errors.New("cannot unfold merge: folded dimension mismatch")
	}
	if a.FoldLevel == 0 {
		return nil, errors.New("cannot unfold merge from fold level 0")
	}
	result, err := NewFoldCountMinSketch(a.Rows, a.FullCols, a.FoldLevel-1)
	if err != nil {
		return nil, err
	}
	a.scatterInto(result)
	b.scatterInto(result)
	result.recomputeNorms()
	return result, nil
}

func HierarchicalMergeFoldCountMinSketch(sketches []*FoldCountMinSketch) (*FoldCountMinSketch, error) {
	if len(sketches) == 0 {
		return nil, errors.New("need at least one folded count-min sketch")
	}
	if len(sketches) == 1 {
		return sketches[0].UnfoldFull()
	}
	rows := sketches[0].Rows
	fullCols := sketches[0].FullCols
	result, err := NewFoldCountMinSketch(rows, fullCols, 0)
	if err != nil {
		return nil, err
	}
	for _, sketch := range sketches {
		if sketch == nil {
			return nil, errors.New("cannot merge nil folded count-min sketch")
		}
		if sketch.Rows != rows || sketch.FullCols != fullCols {
			return nil, errors.New("cannot merge: folded dimension mismatch")
		}
		sketch.scatterInto(result)
	}
	result.recomputeNorms()
	return result, nil
}

func (s *FoldCountMinSketch) ToFlatCounters() []float64 {
	out := make([]float64, s.Rows*s.FullCols)
	for row := 0; row < s.Rows; row++ {
		rowBase := row * s.FullCols
		for col := 0; col < s.FoldCols; col++ {
			s.Cells[row*s.FoldCols+col].Visit(func(fullCol uint32, count float64) {
				out[rowBase+int(fullCol)] += count
			})
		}
	}
	return out
}

func (s *FoldCountMinSketch) Reset() {
	for i := range s.Cells {
		s.Cells[i].Clear()
	}
	clear(s.L1)
	clear(s.L2)
}

func (s *FoldCountMinSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(s)
}

func DeserializeFoldCountMinSketchFromBytes(data []byte) (*FoldCountMinSketch, error) {
	var s FoldCountMinSketch
	if err := common.DecodeFromBytes(data, &s); err != nil {
		return nil, err
	}
	if err := s.rehydrate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *FoldCountMinSketch) scatterInto(target *FoldCountMinSketch) {
	for row := 0; row < s.Rows; row++ {
		srcBase := row * s.FoldCols
		dstBase := row * target.FoldCols
		for col := 0; col < s.FoldCols; col++ {
			s.Cells[srcBase+col].Visit(func(fullCol uint32, count float64) {
				target.Cells[dstBase+target.foldColOf(fullCol)].Insert(fullCol, count)
			})
		}
	}
}

func (s *FoldCountMinSketch) foldColOf(fullCol uint32) int {
	return int(fullCol) & (s.FoldCols - 1)
}

func (s *FoldCountMinSketch) recomputeNorms() {
	for i := range s.L1 {
		s.L1[i] = 0
		s.L2[i] = 0
	}
	for row := 0; row < s.Rows; row++ {
		base := row * s.FoldCols
		for col := 0; col < s.FoldCols; col++ {
			s.Cells[base+col].Visit(func(_ uint32, count float64) {
				s.L1[row] += count
				s.L2[row] += count * count
			})
		}
	}
}
