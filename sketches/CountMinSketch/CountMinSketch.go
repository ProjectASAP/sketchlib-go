package countminsketch

import (
	"errors"
	"math"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// CountMinSketch is a hash-based Count-Min Sketch
// with extended statistics (Count, Sum, Sum2, L1, L2).
// Hashing MUST be done externally (HashLayer).
type CountMinSketch struct {
	Rows  int
	Cols  int
	Count [][]float64
	Sum   [][]float64
	Sum2  [][]float64
	L1    []float64
	L2    []float64
}

/* sketch configurations */
const (
	CM_ROW_NO = 5
	CM_COL_NO = 1000
)

func NewCountMinSketch(row, col int) (*CountMinSketch, error) {
	if row <= 0 || col <= 0 {
		return nil, errors.New("row and col must be positive")
	}

	if row > CM_ROW_NO {
		row = CM_ROW_NO
	}
	if col > CM_COL_NO {
		col = CM_COL_NO
	}

	c := make([][]float64, row)
	s := make([][]float64, row)
	s2 := make([][]float64, row)

	for r := 0; r < row; r++ {
		c[r] = make([]float64, col)
		s[r] = make([]float64, col)
		s2[r] = make([]float64, col)
	}

	return &CountMinSketch{
		Rows:  row,
		Cols:  col,
		Count: c,
		Sum:   s,
		Sum2:  s2,
		L1:    make([]float64, row),
		L2:    make([]float64, row),
	}, nil
}

func (s *CountMinSketch) positionFromHash(h uint64) []int {
	pos := make([]int, s.Rows)
	for i := 0; i < s.Rows; i++ {
		// per-row hash mixing (cheap & deterministic)
		rowHash := h + uint64(i)*0x9e3779b97f4a7c15
		pos[i] = int(rowHash % uint64(s.Cols))
	}
	return pos
}

func (s *CountMinSketch) InsertWithHash(h uint64) {
	pos := s.positionFromHash(h)

	for r, c := range pos {
		cur := s.Count[r][c]

		// frequency
		s.Count[r][c] += 1

		// sum & sum of squares (hash-only path)
		s.Sum[r][c] += 1
		s.Sum2[r][c] += 1

		// norms
		s.L2[r] += s.Count[r][c]*s.Count[r][c] - cur*cur
		s.L1[r] += s.Count[r][c] - cur
	}
}

func (s *CountMinSketch) QueryWithHash(
	q common.QueryType,
	h uint64,
) (float64, error) {

	pos := s.positionFromHash(h)

	switch q {

	case common.QueryFrequency:
		res := math.MaxFloat64
		for r, c := range pos {
			if s.Count[r][c] < res {
				res = s.Count[r][c]
			}
		}
		return res, nil

	case common.QuerySum:
		res := math.MaxFloat64
		idx := 0
		for r, c := range pos {
			v := math.Abs(s.Sum[r][c])
			if v < res {
				res = v
				idx = r
			}
		}
		return s.Sum[idx][pos[idx]], nil

	case common.QuerySum2:
		res := math.MaxFloat64
		for r, c := range pos {
			if s.Sum2[r][c] < res {
				res = s.Sum2[r][c]
			}
		}
		return res, nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (s *CountMinSketch) CM_L1() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		sum := 0.0
		for j := 0; j < s.Cols; j++ {
			sum += s.Count[i][j]
		}
		if sum < res {
			res = sum
		}
	}
	return res
}

func (s *CountMinSketch) CM_L2() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		sum := 0.0
		for j := 0; j < s.Cols; j++ {
			sum += s.Count[i][j] * s.Count[i][j]
		}
		if sum < res {
			res = sum
		}
	}
	return math.Sqrt(res)
}

func (s *CountMinSketch) TypeName() string {
	return "countmin"
}

func (s *CountMinSketch) Merge(other common.Sketch) error {
	o, ok := other.(*CountMinSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}

	if s.Rows != o.Rows || s.Cols != o.Cols {
		return errors.New("cannot merge: dimension mismatch")
	}

	for r := 0; r < s.Rows; r++ {
		s.L1[r] += o.L1[r]
		s.L2[r] += o.L2[r]

		for c := 0; c < s.Cols; c++ {
			s.Count[r][c] += o.Count[r][c]
			s.Sum[r][c] += o.Sum[r][c]
			s.Sum2[r][c] += o.Sum2[r][c]
		}
	}

	return nil
}
