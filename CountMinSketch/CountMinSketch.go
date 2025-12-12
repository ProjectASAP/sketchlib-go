package countminsketch

import (
	"errors"
	"hash"
	"hash/fnv"
	"math"

	"github.com/spaolacci/murmur3"
)

// FIX: Semua field diawali Huruf Besar agar bisa di-serialize oleh Gob
type CountMinSketch struct {
	Rows   int
	Cols   int
	Seed1  []uint32
	Count  [][]float64
	Sum    [][]float64
	Sum2   [][]float64
	L1     []float64
	L2     []float64
	Hasher hash.Hash64
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
		Rows:   row,
		Cols:   col,
		Hasher: fnv.New64(),
	}

	s.Count = make([][]float64, row)
	s.Sum = make([][]float64, row)
	s.Sum2 = make([][]float64, row)
	s.L1 = make([]float64, row)
	s.L2 = make([]float64, row)

	for r := 0; r < row; r++ {
		s.Count[r] = make([]float64, col)
		s.Sum[r] = make([]float64, col)
		s.Sum2[r] = make([]float64, col)
		for c := 0; c < col; c++ {
			s.Count[r][c] = 0
			s.Sum[r][c] = 0
			s.Sum2[r][c] = 0
		}
		s.L1[r] = 0
		s.L2[r] = 0
	}

	s.Seed1 = make([]uint32, row)
	for r := 0; r < row; r++ {
		s.Seed1[r] = seed1[r]
	}

	return s, nil
}

// Accessor methods untuk kompatibilitas
func (s CountMinSketch) Row() int { return s.Rows }
func (s CountMinSketch) Col() int { return s.Cols }

func (s CountMinSketch) position(key []byte) (pos []int) {
	pos = make([]int, s.Rows)
	for i := 0; i < s.Rows; i++ {
		pos[i] = int(murmur3.Sum32WithSeed(key, s.Seed1[i]) % uint32(s.Cols))
	}
	return pos
}

func (s CountMinSketch) CMProcessing(key string, value float64) {
	pos := s.position([]byte(key))
	for r, c := range pos {
		cur_count := s.Count[r][c]
		s.Count[r][c] += 1
		s.Sum[r][c] += value
		s.Sum2[r][c] += value * value
		s.L2[r] += s.Count[r][c]*s.Count[r][c] - cur_count*cur_count
		s.L1[r] += s.Count[r][c] - cur_count
	}
}

func (s CountMinSketch) EstimateStringCount(key string) float64 {
	pos := s.position([]byte(key))
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > s.Count[r][c] {
			res = s.Count[r][c]
		}
	}
	return res
}

func (s CountMinSketch) EstimateStringSum(key string) float64 {
	pos := s.position([]byte(key))
	idx := 0
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > AbsFloat64(s.Sum[r][c]) {
			res = AbsFloat64(s.Sum[r][c])
			idx = r
		}
	}
	return s.Sum[idx][pos[idx]]
}

func (s CountMinSketch) EstimateStringSum2(key string) float64 {
	pos := s.position([]byte(key))
	var res float64 = math.MaxFloat64
	for r, c := range pos {
		if res > s.Sum2[r][c] {
			res = s.Sum2[r][c]
		}
	}
	return res
}
