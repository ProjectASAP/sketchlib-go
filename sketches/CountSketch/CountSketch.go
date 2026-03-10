package countsketch

import (
	"errors"
	"math"
	"math/bits"
	"sort"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

const CS_ROW_NO_Univ_ELEPHANT int = 5
const CS_COL_NO_Univ_ELEPHANT int = 2048
const CS_ROW_NO_Univ_MICE int = 3
const CS_COL_NO_Univ_MICE int = 512

const TOPK_SIZE int = 100
const TOPK_SIZE_MICE int = 100
const TOPK_SIZE2 int = 200

// Default constants if user provides no arguments
const (
	DefaultRows = 5
	DefaultCols = 2048
)

type CountSketch struct {
	Rows int
	Cols int

	countStore *storage.DenseMatrixStorage

	Count [][]float64
	L2    []float64

	// TopK Heap from common package
	TopK *common.TopKHeap

	bitsPerRow uint
}

func (s *CountSketch) rehydrateStorage() error {
	if s.Rows <= 0 || s.Cols <= 0 {
		return errors.New("invalid snapshot dimensions")
	}
	if s.Cols&(s.Cols-1) != 0 {
		return errors.New("invalid snapshot: cols must be power-of-two")
	}
	if len(s.Count) != s.Rows {
		return errors.New("invalid snapshot matrix row count")
	}
	for r := 0; r < s.Rows; r++ {
		if len(s.Count[r]) != s.Cols {
			return errors.New("invalid snapshot matrix col count")
		}
	}
	if len(s.L2) != s.Rows {
		return errors.New("invalid snapshot l2 size")
	}

	if s.TopK == nil {
		s.TopK = common.NewTopKHeap(TOPK_SIZE)
	}

	countStore, err := storage.NewDenseMatrixStorage(s.Rows, s.Cols)
	if err != nil {
		return err
	}
	for r := 0; r < s.Rows; r++ {
		for c := 0; c < s.Cols; c++ {
			countStore.UpdateOneCounter(r, c, s.Count[r][c])
		}
	}

	s.countStore = countStore
	s.Count = countStore.As2D()
	s.bitsPerRow = uint(bits.TrailingZeros(uint(s.Cols)))
	return nil
}

// NewCountSketch creates a new CountSketch.
// Usage:
//
//	NewCountSketch()             -> Uses defaults (5, 2048)
//	NewCountSketch(5, 1024)      -> Uses custom (5, 1024)
func NewCountSketch(dims ...int) (*CountSketch, error) {
	// 1. Determine Dimensions
	var rows, cols int

	switch len(dims) {
	case 0:
		// Case A: User didn't specify dimensions -> Use Defaults
		rows = DefaultRows
		cols = DefaultCols
	case 2:
		// Case B: User specified dimensions -> Use provided values
		rows = dims[0]
		cols = dims[1]
	default:
		return nil, errors.New("invalid usage: NewCountSketch() takes 0 arguments (defaults) or 2 arguments (rows, cols)")
	}

	// 2. Validate Dimensions
	if rows <= 0 || cols <= 0 {
		return nil, errors.New("rows and cols must be positive")
	}

	// Ensure cols is power of two for bitwise masking
	if cols&(cols-1) != 0 {
		return nil, errors.New("cols must be a power of two")
	}

	countStore, err := storage.NewDenseMatrixStorage(rows, cols)
	if err != nil {
		return nil, err
	}
	bitsPerRow := uint(bits.TrailingZeros(uint(cols)))

	// 3. Initialize Structure
	return &CountSketch{
		Rows:       rows,
		Cols:       cols,
		countStore: countStore,
		Count:      countStore.As2D(),
		L2:         make([]float64, rows),
		TopK:       common.NewTopKHeap(TOPK_SIZE), // Default to standard size
		bitsPerRow: bitsPerRow,
	}, nil
}

// derivePosAndSign computes row index and sign (+1/-1) from base hash.
// Kept for backward-compatibility with existing tests.
func (s *CountSketch) derivePosAndSign(hash uint64, row int) (int, float64) {
	hashed := s.countStore.HashForMatrix(hash)
	return s.derivePosAndSignFromHashed(hashed, row)
}

func (s *CountSketch) derivePosAndSignFromHashed(hashed storage.MatrixHashType, row int) (int, float64) {
	col := int(hashed.RowHash(row, s.bitsPerRow, uint64(s.Cols-1)))
	sign := float64(hashed.SignForRow(row))
	return col, sign
}

func (s *CountSketch) fastPacked64PosAndSign(packed uint64, row int) (int, float64) {
	shift := uint(row) * s.bitsPerRow
	col := int((packed >> shift) & uint64(s.Cols-1))
	signBit := (packed >> uint(63-row)) & 1
	sign := -1.0
	if signBit == 1 {
		sign = 1.0
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
	hashed := s.countStore.HashForMatrix(hash)
	count := s.Count
	if hashed.Mode() == storage.MatrixHashPacked64 {
		packed := hashed.Lower64()
		for r := 0; r < s.Rows; r++ {
			c, sign := s.fastPacked64PosAndSign(packed, r)
			increment := sign * value
			row := count[r]
			prev := row[c]
			curr := prev + increment
			row[c] = curr
			s.L2[r] += (curr * curr) - (prev * prev)
		}
		return
	}

	for r := 0; r < s.Rows; r++ {
		c, sign := s.derivePosAndSignFromHashed(hashed, r)
		increment := sign * value

		// Update L2 moments
		row := count[r]
		prev := row[c]
		curr := prev + increment
		row[c] = curr
		s.L2[r] += (curr * curr) - (prev * prev)
	}
}

// QueryWithHash returns the estimated frequency.
func (s *CountSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryFrequency:
		hashed := s.countStore.HashForMatrix(hash)
		var estimatesStack [16]float64
		var estimates []float64
		if s.Rows <= len(estimatesStack) {
			estimates = estimatesStack[:s.Rows]
		} else {
			estimates = make([]float64, s.Rows)
		}
		count := s.Count
		if hashed.Mode() == storage.MatrixHashPacked64 {
			packed := hashed.Lower64()
			for r := 0; r < s.Rows; r++ {
				c, sign := s.fastPacked64PosAndSign(packed, r)
				estimates[r] = count[r][c] * sign
			}
			sort.Float64s(estimates)
			mid := s.Rows / 2
			if s.Rows%2 == 1 {
				return estimates[mid], nil
			}
			return (estimates[mid-1] + estimates[mid]) / 2.0, nil
		}

		for r := 0; r < s.Rows; r++ {
			c, sign := s.derivePosAndSignFromHashed(hashed, r)
			estimates[r] = count[r][c] * sign
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
			s.countStore.UpdateOneCounter(r, c, o.countStore.QueryOneCounter(r, c))
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

// SerializeToBytes serializes CountSketch into bytes.
func (s *CountSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(s)
}

// DeserializeCountSketchFromBytes restores CountSketch from serialized bytes.
func DeserializeCountSketchFromBytes(data []byte) (*CountSketch, error) {
	var s CountSketch
	if err := common.DecodeFromBytes(data, &s); err != nil {
		return nil, err
	}
	if err := s.rehydrateStorage(); err != nil {
		return nil, err
	}
	return &s, nil
}
