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

	if row > 5 {
		row = 5
	}

	s = &CountSketchUniv{
		row: row,
		col: col,
	}

	// Fixed Pool Logic
	if row == CS_ROW_NO_Univ_ELEPHANT && col == CS_COL_NO_Univ_ELEPHANT {
		s.count = iarr2Pool_ele.Get()
		s.l2 = iarrPool_ele.Get()
	} else if row == CS_ROW_NO_Univ_MICE && col == CS_COL_NO_Univ_MICE {
		s.count = iarr2Pool_mice.Get()
		s.l2 = iarrPool_mice.Get()
	} else {
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

func (s *CountSketchUniv) CleanCountSketchUniv() error {
	for r := 0; r < s.row; r++ {
		s.count[r][0] = 0
		for c := 1; c < s.col; c *= 2 {
			copy(s.count[r][c:], s.count[r][:c])
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
		var rowL2 int64 = 0
		for j := 0; j < s.col; j++ {
			s.count[i][j] += o.count[i][j]
			// Recompute L2 sum-of-squares while we iterate
			rowL2 += s.count[i][j] * s.count[i][j]
		}
		// Update the cached L2 value for this row
		s.l2[i] = rowL2
	}
	return nil
}

// --- UnivMon Specific Methods ---

func (s *CountSketchUniv) UpdateWithHash(hash uint64, count int64) {
	for r := 0; r < s.row; r++ {
		idx := common.DeriveIndex(hash, r, uint32(s.col))
		sign := common.DeriveSign(hash, r)

		cur_count := s.count[r][idx]
		s.count[r][idx] += sign * count
		s.l2[r] += s.count[r][idx]*s.count[r][idx] - cur_count*cur_count
	}
}

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

	return &CountSketchUniv{
		row:   snap.Row,
		col:   snap.Col,
		count: snap.Count,
		l2:    snap.L2,
	}, nil
}
