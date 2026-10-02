package univmon

import (
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// MaxRows is the most rows a CountL2HH holds: one per seed in the standard
// seed list.
const MaxRows = 20

// CountL2HH is a Count Sketch of int64 cells whose rows each carry a running
// L2 accumulator. One 128-bit hash of the key at the sketch's seed index
// places it in every row.
type CountL2HH struct {
	rows, cols int
	seedIdx    int
	maskBits   uint
	counts     []int64
	l2         []int64
}

// colsMaskBits is the number of hash bits a row reads its column from:
// ceil(log2(cols)).
func colsMaskBits(cols int) uint { return uint(bits.Len64(uint64(cols) - 1)) }

// checkDimensions rejects a geometry with a zero dimension, more than MaxRows
// rows, a width past u32, or more column and sign bits than one 128-bit hash
// carries.
func checkDimensions(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return fmt.Errorf("univmon: CountL2HH dimensions must be positive: rows=%d, cols=%d", rows, cols)
	}
	if rows > MaxRows {
		return fmt.Errorf("univmon: CountL2HH rows %d exceed %d", rows, MaxRows)
	}
	if uint64(cols) > math.MaxUint32 {
		return fmt.Errorf("univmon: CountL2HH cols %d exceed u32", cols)
	}
	if need := (colsMaskBits(cols) + 1) * uint(rows); need > 128 {
		return fmt.Errorf("univmon: CountL2HH %dx%d needs %d hash bits, more than 128", rows, cols, need)
	}
	return nil
}

// NewCountL2HH returns a zeroed rows x cols sketch hashing at seed index
// seedIdx.
func NewCountL2HH(rows, cols, seedIdx int) (*CountL2HH, error) {
	if err := checkDimensions(rows, cols); err != nil {
		return nil, err
	}
	if seedIdx < 0 {
		return nil, fmt.Errorf("univmon: CountL2HH seed index %d is negative", seedIdx)
	}
	return &CountL2HH{
		rows: rows, cols: cols, seedIdx: seedIdx,
		maskBits: colsMaskBits(cols),
		counts:   make([]int64, rows*cols),
		l2:       make([]int64, rows),
	}, nil
}

// Rows returns the row count.
func (s *CountL2HH) Rows() int { return s.rows }

// Cols returns the column count.
func (s *CountL2HH) Cols() int { return s.cols }

// SeedIndex returns the seed-list index the sketch hashes with.
func (s *CountL2HH) SeedIndex() int { return s.seedIdx }

// cell returns row's column and sign for a key hash h: the column from bits
// [row*maskBits, (row+1)*maskBits) folded % cols, the sign from bit 127-row.
func (s *CountL2HH) cell(h common.Hash128, row int) (int, int64) {
	field := shr128(h, uint(row)*s.maskBits) & (1<<s.maskBits - 1)
	col := int(field % uint64(s.cols))
	if (h.Hi>>(63-uint(row)))&1 == 1 {
		return col, 1
	}
	return col, -1
}

func shr128(h common.Hash128, n uint) uint64 {
	switch {
	case n == 0:
		return h.Lo
	case n < 64:
		return h.Lo>>n | h.Hi<<(64-n)
	default:
		return h.Hi >> (n - 64)
	}
}

func (s *CountL2HH) hash(key []byte) common.Hash128 { return common.Hash128It(s.seedIdx, key) }

// insert adds sign*c to the key's cell in every row and carries each row's
// L2 accumulator, clamped to [0, MaxInt64].
func (s *CountL2HH) insert(h common.Hash128, c int64) {
	for r := range s.rows {
		col, sign := s.cell(h, r)
		i := r*s.cols + col
		old := s.counts[i]
		next := old + sign*c
		s.counts[i] = next
		s.l2[r] = carryL2(s.l2[r], old, next)
	}
}

// carryL2 returns acc + next^2 - old^2 in 128-bit arithmetic, clamped to
// [0, MaxInt64].
func carryL2(acc, old, next int64) int64 {
	nh, nl := square(next)
	oh, ol := square(old)
	lo, carry := bits.Add64(uint64(acc), nl, 0)
	hi, _ := bits.Add64(0, nh, carry)
	lo, borrow := bits.Sub64(lo, ol, 0)
	hi, _ = bits.Sub64(hi, oh, borrow)
	switch {
	case int64(hi) < 0:
		return 0
	case hi != 0 || lo > math.MaxInt64:
		return math.MaxInt64
	default:
		return int64(lo)
	}
}

func square(v int64) (hi, lo uint64) {
	a := uint64(v)
	if v < 0 {
		a = -a
	}
	return bits.Mul64(a, a)
}

// estimate returns the median over rows of sign*cell for a key hash h.
func (s *CountL2HH) estimate(h common.Hash128) float64 {
	vals := make([]float64, s.rows)
	for r := range s.rows {
		col, sign := s.cell(h, r)
		vals[r] = float64(sign * s.counts[r*s.cols+col])
	}
	return median(vals)
}

// Estimate returns the key's frequency estimate.
func (s *CountL2HH) Estimate(key []byte) float64 { return s.estimate(s.hash(key)) }

// L2 returns the square root of the median row accumulator.
func (s *CountL2HH) L2() float64 {
	vals := make([]float64, s.rows)
	for r, v := range s.l2 {
		vals[r] = float64(v)
	}
	return math.Sqrt(median(vals))
}

// median returns the middle value of vals, or the mean of the two middle
// values when there is an even number of them.
func median(vals []float64) float64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	slices.Sort(vals)
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}

// merge adds o's cells into s and sets each row's accumulator to the row's
// sum of squares, saturated at MaxInt64.
func (s *CountL2HH) merge(o *CountL2HH) {
	for r := range s.rows {
		var acc uint64
		for c := range s.cols {
			i := r*s.cols + c
			s.counts[i] += o.counts[i]
			hi, lo := square(s.counts[i])
			if hi != 0 || lo > math.MaxInt64 {
				acc = math.MaxInt64
				continue
			}
			acc += lo
			if acc > math.MaxInt64 {
				acc = math.MaxInt64
			}
		}
		s.l2[r] = int64(acc)
	}
}

func (s *CountL2HH) clear() {
	clear(s.counts)
	clear(s.l2)
}
