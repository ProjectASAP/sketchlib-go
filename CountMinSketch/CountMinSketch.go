package countminsketch

import (
	"math"
	// "math/rand"
	"errors"
	"hash"
	"hash/fnv"

	// "time"
	"github.com/spaolacci/murmur3"
)

type CountMinSketch struct {
	row      int
	col      int
	seed1    []uint32
	count    [][]float64
	sum      [][]float64
	sum2     [][]float64
	l1       []float64
	l2       []float64
	hasher   hash.Hash64
}

/* sketch configurations */
const CM_ROW_NO int = 5
const CM_COL_NO int = 1000

var SEEDLIST [6]uint64 = [6]uint64{0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5, 0x6a09e667}

func AbsFloat64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func NewCountMinSketch(row, col int, seed1 []uint32) (s CountMinSketch, err error) {
	if row <= 0 || col <= 0 {
		return CountMinSketch{}, errors.New("CountMinSketch New: values of row and col should be positive.")
	}

	if len(seed1) < row {
		adjusted := make([]uint32, row)
		copy(adjusted, seed1)
		for r := len(seed1); r < row; r++ {
			adjusted[r] = uint32(SEEDLIST[r%len(SEEDLIST)])
		}
		seed1 = adjusted
	}

	if row > CM_ROW_NO {
		row = CM_ROW_NO
	}

	if col > CM_COL_NO {
		col = CM_COL_NO
	}

	s = CountMinSketch{
		row:    row,
		col:    col,
		hasher: fnv.New64(),
	}

	s.count = make([][]float64, row)
	s.sum = make([][]float64, row)
	s.sum2 = make([][]float64, row)
	s.l1 = make([]float64, row)
	s.l2 = make([]float64, row)

	for r := 0; r < row; r++ {
		s.count[r] = make([]float64, col)
		s.sum[r] = make([]float64, col)
		s.sum2[r] = make([]float64, col)
		for c := 0; c < col; c++ {
			s.count[r][c] = 0
			s.sum[r][c] = 0
			s.sum2[r][c] = 0
		}
		s.l1[r] = 0
		s.l2[r] = 0
	}

	s.seed1 = make([]uint32, row)
	for r := 0; r < row; r++ {
		s.seed1[r] = seed1[r]
	}
	/*
		rand.Seed(time.Now().UnixNano())
		for r := 0; r < row; r++ {
			s.seed1[r] = rand.Uint32()
		}
	*/

	return s, nil
}

func (s CountMinSketch) FreeCountSketch() error { return nil }

// Row returns the number of rows (hash functions)
func (s CountMinSketch) Row() int { return s.row }

// Col returns the number of columns
func (s CountMinSketch) Col() int { return s.col }

func (s CountMinSketch) position(key []byte) (pos []int) {
	pos = make([]int, s.row)
	for i := 0; i < s.row; i++ {
		pos[i] = int(murmur3.Sum32WithSeed(key, s.seed1[i]) % uint32(s.col))
	}
	return pos
}

func (s CountMinSketch) CMProcessing(key string, value float64) {
	// line_to_update := s.line_to_update
	// col_loc := xxhash.Sum64String(key) % uint64(CM_COL_NO)
	// s.count[line_to_update][col_loc] += value // value is 1 for frequency
	pos := s.position([]byte(key))
	for r, c := range pos {
		cur_count := s.count[r][c]
		s.count[r][c] += 1
		s.sum[r][c] += value
		s.sum2[r][c] += value * value
		s.l2[r] += s.count[r][c]*s.count[r][c] - cur_count*cur_count
		s.l1[r] += s.count[r][c] - cur_count
	}
}

func (s CountMinSketch) EstimateStringCount(key string) float64 {
	pos := s.position([]byte(key))
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > s.count[r][c] {
			res = s.count[r][c]
		}
	}
	return res
}

func (s CountMinSketch) EstimateStringSum(key string) float64 {
	pos := s.position([]byte(key))
	idx := 0
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > AbsFloat64(s.sum[r][c]) {
			res = AbsFloat64(s.sum[r][c])
			idx = r
		}
	}
	return s.sum[idx][pos[idx]]
}

func (s CountMinSketch) EstimateStringSum2(key string) float64 {
	pos := s.position([]byte(key))
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > s.sum2[r][c] {
			res = s.sum2[r][c]
		}
	}
	return res
}

func (s CountMinSketch) cm_l1() float64 {
	var res float64 = math.MaxFloat64
	var tmp_sum float64
	for i := 0; i < s.row; i++ {
		tmp_sum = 0
		for j := 0; j < CM_COL_NO; j++ {
			tmp_sum += s.count[i][j]
		}
		if res > tmp_sum {
			res = tmp_sum
		}
	}
	return res
}

func (s CountMinSketch) cm_l2() float64 {
	var res float64 = math.MaxFloat64
	var tmp_sq_sum float64
	for i := 0; i < CM_ROW_NO; i++ {
		tmp_sq_sum = 0
		for j := 0; j < CM_COL_NO; j++ {
			tmp_sq_sum += s.count[i][j] * s.count[i][j]
		}
		if res > tmp_sq_sum {
			res = tmp_sq_sum
		}
	}
	return math.Sqrt(res)
}
