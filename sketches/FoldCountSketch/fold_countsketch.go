package foldcountsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	foldcommon "github.com/ProjectASAP/sketchlib-go/sketches/FoldCommon"
)

type FoldCountSketch struct {
	Rows      int
	FoldCols  int
	FullCols  int
	FoldLevel uint32
	Cells     []foldcommon.FoldCell
	L2        []float64

	bitsPerRow uint
	mask       uint64
}

func NewFoldCountSketch(rows, fullCols int, foldLevel uint32) (*FoldCountSketch, error) {
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

	s := &FoldCountSketch{
		Rows:      rows,
		FoldCols:  fullCols >> foldLevel,
		FullCols:  fullCols,
		FoldLevel: foldLevel,
		Cells:     make([]foldcommon.FoldCell, rows*(fullCols>>foldLevel)),
		L2:        make([]float64, rows),
	}
	s.bitsPerRow = uint(bits.TrailingZeros(uint(fullCols)))
	s.mask = uint64(fullCols - 1)
	return s, nil
}

func NewFoldCountSketchFull(rows, fullCols int) (*FoldCountSketch, error) {
	return NewFoldCountSketch(rows, fullCols, 0)
}

func (s *FoldCountSketch) rehydrate() error {
	if s.Rows <= 0 || s.FullCols <= 0 || s.FoldCols <= 0 {
		return errors.New("invalid folded count-sketch dimensions")
	}
	if s.FullCols&(s.FullCols-1) != 0 {
		return errors.New("invalid folded count-sketch fullCols")
	}
	if s.FoldCols != s.FullCols>>s.FoldLevel {
		return errors.New("invalid folded count-sketch foldCols")
	}
	if len(s.Cells) != s.Rows*s.FoldCols {
		return errors.New("invalid folded count-sketch cell count")
	}
	if len(s.L2) != s.Rows {
		return errors.New("invalid folded count-sketch norm size")
	}
	s.bitsPerRow = uint(bits.TrailingZeros(uint(s.FullCols)))
	s.mask = uint64(s.FullCols - 1)
	return nil
}

func (s *FoldCountSketch) TypeName() string { return "fold_countsketch" }

func (s *FoldCountSketch) RowCount() int  { return s.Rows }
func (s *FoldCountSketch) ColCount() int  { return s.FullCols }
func (s *FoldCountSketch) CellCount() int { return len(s.Cells) }

func (s *FoldCountSketch) TotalEntries() int {
	total := 0
	for i := range s.Cells {
		total += s.Cells[i].EntryCount()
	}
	return total
}

func (s *FoldCountSketch) CollidedCells() int {
	total := 0
	for i := range s.Cells {
		if s.Cells[i].EntryCount() > 1 {
			total++
		}
	}
	return total
}

func (s *FoldCountSketch) InsertWithHash(hash uint64) {
	s.insertHashed(storage.BuildMatrixHash(hash, s.Rows, s.FullCols), 1)
}

func (s *FoldCountSketch) Insert(input *common.SketchInput) {
	if input == nil {
		return
	}
	s.insertHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols), 1)
}

func (s *FoldCountSketch) InsertWeight(input *common.SketchInput, many float64) {
	if input == nil || many == 0 {
		return
	}
	s.insertHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols), many)
}

func (s *FoldCountSketch) insertHashed(hashed storage.MatrixHashType, many float64) {
	for row := 0; row < s.Rows; row++ {
		fullCol := uint32(hashed.RowHash(row, s.bitsPerRow, s.mask))
		sign := float64(hashed.SignForRow(row))
		cellIdx := row*s.FoldCols + s.foldColOf(fullCol)
		prev := s.Cells[cellIdx].Query(fullCol)
		delta := sign * many
		s.Cells[cellIdx].Insert(fullCol, delta)
		curr := prev + delta
		s.L2[row] += curr*curr - prev*prev
	}
}

func (s *FoldCountSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryFrequency:
		return s.queryHashed(storage.BuildMatrixHash(hash, s.Rows, s.FullCols)), nil
	case common.QuerySum2:
		var estimates []float64
		if s.Rows <= 16 {
			var stack [16]float64
			estimates = stack[:s.Rows]
			copy(estimates, s.L2)
			return math.Sqrt(common.ComputeMedianInlineF64(estimates)), nil
		}
		estimates = append(make([]float64, 0, s.Rows), s.L2...)
		return math.Sqrt(common.ComputeMedianInlineF64(estimates)), nil
	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (s *FoldCountSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	return s.queryHashed(storage.BuildMatrixHashFromInput(input, s.Rows, s.FullCols))
}

func (s *FoldCountSketch) queryHashed(hashed storage.MatrixHashType) float64 {
	var estimates []float64
	if s.Rows <= 16 {
		var stack [16]float64
		estimates = stack[:s.Rows]
		for row := 0; row < s.Rows; row++ {
			fullCol := uint32(hashed.RowHash(row, s.bitsPerRow, s.mask))
			sign := float64(hashed.SignForRow(row))
			estimates[row] = sign * s.Cells[row*s.FoldCols+s.foldColOf(fullCol)].Query(fullCol)
		}
		return common.ComputeMedianInlineF64(estimates)
	}
	estimates = make([]float64, s.Rows)
	for row := 0; row < s.Rows; row++ {
		fullCol := uint32(hashed.RowHash(row, s.bitsPerRow, s.mask))
		sign := float64(hashed.SignForRow(row))
		estimates[row] = sign * s.Cells[row*s.FoldCols+s.foldColOf(fullCol)].Query(fullCol)
	}
	return common.ComputeMedianInlineF64(estimates)
}

func (s *FoldCountSketch) Merge(other common.Sketch) error {
	o, ok := other.(*FoldCountSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	return s.MergeSameLevel(o)
}

func (s *FoldCountSketch) MergeSameLevel(other *FoldCountSketch) error {
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

func (s *FoldCountSketch) UnfoldTo(targetLevel uint32) (*FoldCountSketch, error) {
	if targetLevel > s.FoldLevel {
		return nil, errors.New("target level must be <= current fold level")
	}
	if targetLevel == s.FoldLevel {
		clone := *s
		clone.Cells = append([]foldcommon.FoldCell(nil), s.Cells...)
		clone.L2 = append([]float64(nil), s.L2...)
		return &clone, nil
	}
	target, err := NewFoldCountSketch(s.Rows, s.FullCols, targetLevel)
	if err != nil {
		return nil, err
	}
	s.scatterInto(target)
	target.recomputeNorms()
	return target, nil
}

func (s *FoldCountSketch) UnfoldFull() (*FoldCountSketch, error) {
	return s.UnfoldTo(0)
}

func UnfoldMergeFoldCountSketch(a, b *FoldCountSketch) (*FoldCountSketch, error) {
	if a == nil || b == nil {
		return nil, errors.New("cannot unfold merge nil sketch")
	}
	if a.Rows != b.Rows || a.FullCols != b.FullCols || a.FoldLevel != b.FoldLevel {
		return nil, errors.New("cannot unfold merge: folded dimension mismatch")
	}
	if a.FoldLevel == 0 {
		return nil, errors.New("cannot unfold merge from fold level 0")
	}
	result, err := NewFoldCountSketch(a.Rows, a.FullCols, a.FoldLevel-1)
	if err != nil {
		return nil, err
	}
	a.scatterInto(result)
	b.scatterInto(result)
	result.recomputeNorms()
	return result, nil
}

func HierarchicalMergeFoldCountSketch(sketches []*FoldCountSketch) (*FoldCountSketch, error) {
	if len(sketches) == 0 {
		return nil, errors.New("need at least one folded count-sketch")
	}
	if len(sketches) == 1 {
		return sketches[0].UnfoldFull()
	}
	rows := sketches[0].Rows
	fullCols := sketches[0].FullCols
	result, err := NewFoldCountSketch(rows, fullCols, 0)
	if err != nil {
		return nil, err
	}
	for _, sketch := range sketches {
		if sketch == nil {
			return nil, errors.New("cannot merge nil folded count-sketch")
		}
		if sketch.Rows != rows || sketch.FullCols != fullCols {
			return nil, errors.New("cannot merge: folded dimension mismatch")
		}
		sketch.scatterInto(result)
	}
	result.recomputeNorms()
	return result, nil
}

func (s *FoldCountSketch) ToFlatCounters() []float64 {
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

func (s *FoldCountSketch) Reset() {
	for i := range s.Cells {
		s.Cells[i].Clear()
	}
	clear(s.L2)
}

func (s *FoldCountSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(s)
}

func DeserializeFoldCountSketchFromBytes(data []byte) (*FoldCountSketch, error) {
	var s FoldCountSketch
	if err := common.DecodeFromBytes(data, &s); err != nil {
		return nil, err
	}
	if err := s.rehydrate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *FoldCountSketch) scatterInto(target *FoldCountSketch) {
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

func (s *FoldCountSketch) foldColOf(fullCol uint32) int {
	return int(fullCol) & (s.FoldCols - 1)
}

func (s *FoldCountSketch) recomputeNorms() {
	clear(s.L2)
	for row := 0; row < s.Rows; row++ {
		base := row * s.FoldCols
		for col := 0; col < s.FoldCols; col++ {
			s.Cells[base+col].Visit(func(_ uint32, count float64) {
				s.L2[row] += count * count
			})
		}
	}
}
