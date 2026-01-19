package countsketch

import (
	"errors"
	"math"
	"math/bits"
	"sort"

	"github.com/approx-telemetry/sketchlib-go/common"
)

const CS_ROW_NO_Univ_ELEPHANT int = 5
const CS_COL_NO_Univ_ELEPHANT int = 4096
const CS_ROW_NO_Univ_MICE int = 3
const CS_COL_NO_Univ_MICE int = 512

const TOPK_SIZE int = 100
const TOPK_SIZE_MICE int = 100
const TOPK_SIZE2 int = 200

type CountSketch struct {
	Rows int
	Cols int

	Count [][]float64
	L2    []float64

	// TopK Heap from common package
	TopK *common.TopKHeap

	// Bit manipulation for fast index/sign derivation
	bitsPerRow uint
	mask       uint64
}

// NewCountSketch creates a new CountSketch.
// Rows and Cols are configurable, but constants are provided for standard sizes.
func NewCountSketch(rows, cols int) (*CountSketch, error) {
	if rows <= 0 || cols <= 0 {
		return nil, errors.New("rows and cols must be positive")
	}

	// Ensure cols is power of two for bitwise masking
	if cols&(cols-1) != 0 {
		return nil, errors.New("cols must be a power of two")
	}

	count := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		count[r] = make([]float64, cols)
	}

	// Calculate bits needed per row to derive column index
	bitsPerRow := uint(bits.TrailingZeros(uint(cols)))

	// Check if 64-bit hash is sufficient
	// Need: rows * (bitsPerRow + 1_sign_bit)
	totalBitsNeeded := uint(rows) * (bitsPerRow + 1)
	if totalBitsNeeded > 64 {
		return nil, errors.New("parameters too large for single 64-bit hash derivation")
	}

	return &CountSketch{
		Rows:       rows,
		Cols:       cols,
		Count:      count,
		L2:         make([]float64, rows),
		TopK:       common.NewTopKHeap(TOPK_SIZE), // Default to standard size
		bitsPerRow: bitsPerRow,
		mask:       uint64(cols - 1),
	}, nil
}

// derivePosAndSign computes row index and sign (+1/-1) from the base hash.
func (s *CountSketch) derivePosAndSign(hash uint64, row int) (int, float64) {
	// 1. Derive Index (Column)
	shiftIdx := uint(row) * s.bitsPerRow
	col := int((hash >> shiftIdx) & s.mask)

	// 2. Derive Sign
	// Use high bits for sign to avoid overlap with index bits
	// (63 - row) ensures we use unique bits for each row's sign
	shiftSign := uint(63 - row)
	signBit := (hash >> shiftSign) & 1

	sign := 1.0
	if signBit == 0 {
		sign = -1.0
	}

	return col, sign
}

// InsertWithHash inserts a value using pre-calculated hash.
// NOTE: This path CANNOT update TopK because the key string is missing.
// Use UpdateString if TopK is required.
func (s *CountSketch) InsertWithHash(hash uint64) {
	s.InsertWithHashAndValue(hash, 1.0)
}

// InsertWithHashAndValue supports weighted updates.
func (s *CountSketch) InsertWithHashAndValue(hash uint64, value float64) {
	for r := 0; r < s.Rows; r++ {
		c, sign := s.derivePosAndSign(hash, r)
		increment := sign * value

		// Update L2 moments
		prev := s.Count[r][c]
		s.Count[r][c] += increment
		s.L2[r] += (s.Count[r][c] * s.Count[r][c]) - (prev * prev)
	}
}

// QueryWithHash returns the estimated frequency.
func (s *CountSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryFrequency:
		estimates := make([]float64, s.Rows)
		for r := 0; r < s.Rows; r++ {
			c, sign := s.derivePosAndSign(hash, r)
			estimates[r] = s.Count[r][c] * sign
		}
		sort.Float64s(estimates)

		// Return Median
		mid := s.Rows / 2
		if s.Rows%2 == 1 {
			return estimates[mid], nil
		}
		return (estimates[mid-1] + estimates[mid]) / 2.0, nil

	case common.QuerySum2:
		// Return Median of L2 arrays
		l2s := make([]float64, s.Rows)
		copy(l2s, s.L2)
		sort.Float64s(l2s)

		mid := s.Rows / 2
		val := l2s[mid]
		if s.Rows%2 == 0 {
			val = (l2s[mid-1] + l2s[mid]) / 2.0
		}
		return math.Sqrt(val), nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (s *CountSketch) Merge(other common.Sketch) error {
	o, ok := other.(*CountSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	if s.Rows != o.Rows || s.Cols != o.Cols {
		return errors.New("cannot merge: dimension mismatch")
	}

	// 1. Merge Matrix and L2
	for r := 0; r < s.Rows; r++ {
		s.L2[r] += o.L2[r]
		for c := 0; c < s.Cols; c++ {
			s.Count[r][c] += o.Count[r][c]
		}
	}

	// 2. Merge TopK Heap
	if s.TopK != nil && o.TopK != nil {
		for _, item := range o.TopK.Heap {
			s.TopK.UpdateCS(item.Key, item.Count)
		}
	}

	return nil
}

func (s *CountSketch) TypeName() string {
	return "countsketch"
}

// ================= EXTENDED FUNCTIONALITY (TopK Support) =================

// UpdateString updates the sketch AND the TopK heap.
// This preserves the functionality of the original implementation.
func (s *CountSketch) UpdateString(key string, count float64) {
	// 1. Compute Hash internally (or accept it as arg if preferred)
	hash := common.Hash64([]byte(key))

	// 2. Insert into Sketch
	s.InsertWithHashAndValue(hash, count)

	// 3. Estimate current count to update Heap
	est, _ := s.QueryWithHash(common.QueryFrequency, hash)

	// 4. Update TopK Heap
	// We cast to int64 because common.TopKHeap uses int64
	s.TopK.UpdateCS(key, int64(est))
}

// EstimateStringCount is a helper to query by string directly
func (s *CountSketch) EstimateStringCount(key string) int64 {
	hash := common.Hash64([]byte(key))
	est, _ := s.QueryWithHash(common.QueryFrequency, hash)
	return int64(est)
}
