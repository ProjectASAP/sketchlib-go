package univmon

import (
	"errors"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// CountSketchUniv implements common.Sketch
type CountSketchUniv struct {
	row        int
	col        int
	countStore *storage.Vector2D[int64]
	l2Store    *storage.Vector1D[int64]
	count      [][]int64
	l2         []int64
}

// NewCountSketchUniv no longer requires manual seeds
func NewCountSketchUniv(row int, col int) (s *CountSketchUniv, err error) {
	if row <= 0 || col <= 0 {
		return nil, errors.New("CountSketchUniv New: values of row and col should be positive")
	}

	if row > 5 {
		row = 5
	}

	s = &CountSketchUniv{
		row: row,
		col: col,
	}

	countStore, err := storage.InitVector2D[int64](row, col)
	if err != nil {
		return nil, err
	}
	l2Store, err := storage.FilledVector1D[int64](row, 0)
	if err != nil {
		return nil, err
	}
	s.countStore = countStore
	s.l2Store = l2Store
	s.count = countStore.As2D()
	s.l2 = l2Store.AsSlice()

	return s, nil
}

func (s *CountSketchUniv) TypeName() string {
	return "CountSketchUniv"
}

func (s *CountSketchUniv) CleanCountSketchUniv() error {
	for r := 0; r < s.row; r++ {
		row := s.countStore.RowSlice(r)
		for c := 0; c < s.col; c++ {
			row[c] = 0
		}
		s.l2[r] = 0
	}
	return nil
}

func (s *CountSketchUniv) InsertWithHash(hash uint64) {
	s.UpdateWithHash(hash, 1)
}

func (s *CountSketchUniv) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryFrequency || q == common.QuerySum {
		return float64(s.EstimateHash(hash)), nil
	}
	if q == common.QuerySum2 {
		return s.cs_l2(), nil
	}
	return 0, common.ErrUnsupportedQuery
}

// Merge combines another sketch into this one.
// FIX: We must recalculate the L2 norm row by row.
func (s *CountSketchUniv) Merge(other common.Sketch) error {
	o, ok := other.(*CountSketchUniv)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}

	if s.row != o.row || s.col != o.col {
		return errors.New("dimension mismatch")
	}

	for i := 0; i < s.row; i++ {
		row := s.countStore.RowSlice(i)
		otherRow := o.countStore.RowSlice(i)
		var rowL2 int64 = 0
		for j := 0; j < s.col; j++ {
			row[j] += otherRow[j]
			// Recompute L2 sum-of-squares while we iterate
			rowL2 += row[j] * row[j]
		}
		// Update the cached L2 value for this row
		s.l2[i] = rowL2
	}
	return nil
}

// --- UnivMon Specific Methods ---

func (s *CountSketchUniv) UpdateWithHash(hash uint64, count int64) {
	l2 := s.l2
	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		row := s.countStore.RowSlice(r)

		curCount := row[idx]
		nextCount := curCount + sign*count
		row[idx] = nextCount
		l2[r] += nextCount*nextCount - curCount*curCount
	}
}

// UpdateWithHashNoL2 updates counters without maintaining L2 cache.
// Used by non-root UnivMon layers for faster update throughput.
func (s *CountSketchUniv) UpdateWithHashNoL2(hash uint64, count int64) {
	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		row := s.countStore.RowSlice(r)
		row[idx] += sign * count
	}
}

func (s *CountSketchUniv) UpdateAndEstimateHash(hash uint64, count int64) int64 {
	var counters [3]int64
	limit := s.row
	if limit > 3 {
		limit = 3
	}

	for r := 0; r < limit; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		row := s.countStore.RowSlice(r)

		curCount := row[idx]
		nextCount := curCount + sign*count
		row[idx] = nextCount
		s.l2[r] += nextCount*nextCount - curCount*curCount

		counters[r] = sign * nextCount
	}

	return MedianOfThree(counters[0], counters[1], counters[2])
}

func (s *CountSketchUniv) UpdateAndEstimateHashNoL2(hash uint64, count int64) int64 {
	var counters [3]int64
	limit := s.row
	if limit > 3 {
		limit = 3
	}
	for r := 0; r < limit; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		row := s.countStore.RowSlice(r)

		row[idx] += sign * count
		counters[r] = sign * row[idx]
	}
	return MedianOfThree(counters[0], counters[1], counters[2])
}

func (s *CountSketchUniv) EstimateHash(hash uint64) int64 {
	var counters [3]int64
	limit := s.row
	if limit > 3 {
		limit = 3
	}
	for r := 0; r < limit; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		row := s.countStore.RowSlice(r)
		counters[r] = sign * row[idx]
	}
	return MedianOfThree(counters[0], counters[1], counters[2])
}

func (s *CountSketchUniv) cs_l2() float64 {
	f2_value := MedianOfThree(s.l2[0], s.l2[1], s.l2[2])
	return math.Sqrt(float64(f2_value))
}

type countSketchUnivSnapshot struct {
	Row   int
	Col   int
	Count [][]int64
	L2    []int64
}

// SerializeToBytes serializes CountSketchUniv into bytes.
func (s *CountSketchUniv) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(countSketchUnivSnapshot{
		Row:   s.row,
		Col:   s.col,
		Count: s.count,
		L2:    s.l2,
	})
}

// DeserializeCountSketchUnivFromBytes restores CountSketchUniv from serialized bytes.
func DeserializeCountSketchUnivFromBytes(data []byte) (*CountSketchUniv, error) {
	var snap countSketchUnivSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.Row <= 0 || snap.Col <= 0 {
		return nil, errors.New("invalid snapshot dimensions")
	}
	if len(snap.Count) != snap.Row {
		return nil, errors.New("invalid snapshot count rows")
	}
	for i := 0; i < snap.Row; i++ {
		if len(snap.Count[i]) != snap.Col {
			return nil, errors.New("invalid snapshot count cols")
		}
	}
	if len(snap.L2) != snap.Row {
		return nil, errors.New("invalid snapshot l2 length")
	}

	countStore, err := storage.Vector2DFrom2D[int64](snap.Count)
	if err != nil {
		return nil, err
	}
	l2Store := storage.Vector1DFromSlice(snap.L2)

	return &CountSketchUniv{
		row:        snap.Row,
		col:        snap.Col,
		countStore: countStore,
		l2Store:    l2Store,
		count:      countStore.As2D(),
		l2:         l2Store.AsSlice(),
	}, nil
}
