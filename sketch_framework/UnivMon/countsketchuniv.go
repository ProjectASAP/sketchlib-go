package univmon

import (
	"errors"
	"math"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// CountSketchUniv implements common.Sketch
type CountSketchUniv struct {
	row   int
	col   int
	count [][]int64
	l2    []int64
}

// NewCountSketchUniv no longer requires manual seeds
func NewCountSketchUniv(row int, col int) (s *CountSketchUniv, err error) {
	if row <= 0 || col <= 0 {
		return nil, errors.New("CountSketchUniv New: values of row and col should be positive")
	}

	// Limit row limit removed (because test cases might request 5, don't force down to 5 if pool logic is correct).
	// However, common.DeriveIndex indeed has efficiency limits.
	// We leave this limit if it is indeed needed for hashing, but ensure memory allocation is correct.
	if row > 5 {
		row = 5
	}

	s = &CountSketchUniv{
		row: row,
		col: col,
	}

	// Fixed Pool Logic: Check Row AND Col
	if row == CS_ROW_NO_Univ_ELEPHANT && col == CS_COL_NO_Univ_ELEPHANT {
		s.count = iarr2Pool_ele.Get()
		s.l2 = iarrPool_ele.Get()
	} else if row == CS_ROW_NO_Univ_MICE && col == CS_COL_NO_Univ_MICE {
		s.count = iarr2Pool_mice.Get()
		s.l2 = iarrPool_mice.Get()
	} else {
		// Fallback: If custom size (like in unit tests), create new array manually
		s.count = make([][]int64, row)
		for r := 0; r < row; r++ {
			s.count[r] = make([]int64, col)
		}
		s.l2 = make([]int64, row)
	}

	return s, nil
}
func (s *CountSketchUniv) TypeName() string {
	return "CountSketchUniv"
}

// CleanCountSketchUniv resets counters
func (s *CountSketchUniv) CleanCountSketchUniv() error {
	for r := 0; r < s.row; r++ {
		s.count[r][0] = 0
		// Array zeroing optimization
		for c := 1; c < s.col; c *= 2 {
			copy(s.count[r][c:], s.count[r][:c])
		}
		s.l2[r] = 0
	}
	return nil
}

// InsertWithHash implements the common.Sketch interface
// Default value = 1
func (s *CountSketchUniv) InsertWithHash(hash uint64) {
	s.UpdateWithHash(hash, 1)
}

// QueryWithHash implements the common.Sketch interface
// Returns Frequency Count estimation
func (s *CountSketchUniv) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryFrequency {
		return float64(s.EstimateHash(hash)), nil
	}
	if q == common.QuerySum2 {
		return s.cs_l2(), nil
	}
	return 0, common.ErrUnsupportedQuery
}

func (s *CountSketchUniv) Merge(other common.Sketch) error {
	// Casting check
	o, ok := other.(*CountSketchUniv)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}

	if s.row != o.row || s.col != o.col {
		return errors.New("dimension mismatch")
	}

	for i := 0; i < s.row; i++ {
		for j := 0; j < s.col; j++ {
			s.count[i][j] += o.count[i][j]
		}
	}
	return nil
}

// --- UnivMon Specific Methods (Weighted & Fused Update/Estimate) ---

// UpdateWithHash performs an update with a specific value (weighted)
func (s *CountSketchUniv) UpdateWithHash(hash uint64, count int64) {
	for r := 0; r < s.row; r++ {
		// Use common.DeriveIndex & DeriveSign
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)

		cur_count := s.count[r][idx]
		s.count[r][idx] += sign * count
		s.l2[r] += s.count[r][idx]*s.count[r][idx] - cur_count*cur_count
	}
}

// UpdateAndEstimateHash is the core of the UnivMon algorithm: update then return estimate
// to update the Heavy Hitter Heap in that layer.
func (s *CountSketchUniv) UpdateAndEstimateHash(hash uint64, count int64) int64 {
	counters := make([]int64, s.row)

	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)

		cur_count := s.count[r][idx]
		s.count[r][idx] += sign * count
		s.l2[r] += s.count[r][idx]*s.count[r][idx] - cur_count*cur_count

		counters[r] = sign * s.count[r][idx]
	}

	return MedianOfThree(counters[0], counters[1], counters[2])
}

// Version without L2 update (for optimization in upper UnivMon layers)
func (s *CountSketchUniv) UpdateAndEstimateHashNoL2(hash uint64, count int64) int64 {
	counters := make([]int64, s.row)
	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)

		s.count[r][idx] += sign * count
		counters[r] = sign * s.count[r][idx]
	}
	return MedianOfThree(counters[0], counters[1], counters[2])
}

func (s *CountSketchUniv) EstimateHash(hash uint64) int64 {
	counters := make([]int64, s.row)
	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)
		counters[r] = sign * s.count[r][idx]
	}
	return MedianOfThree(counters[0], counters[1], counters[2])
}

// Helper to calculate L2 Norm
func (s *CountSketchUniv) cs_l2() float64 {
	f2_value := MedianOfThree(s.l2[0], s.l2[1], s.l2[2])
	return math.Sqrt(float64(f2_value))
}
