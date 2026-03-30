package countsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	spacesaving "github.com/ProjectASAP/sketchlib-go/sketches/SpaceSaving"
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
	DefaultRows     = 5
	DefaultCols     = 2048
	RustDefaultRows = 3
	RustDefaultCols = 4096
)

type CountSketch struct {
	Rows int
	Cols int

	countStore *storage.FlatVector2D

	Count [][]float64
	L2    []float64

	// TopK is the downstream query heap, rebuilt from hh_keys + CS estimates
	// on each ApplyDelta call. Populated only at receiving nodes.
	TopK *common.TopKHeap

	// SS is the upstream insert tracker (Weighted Space Saving).
	// Maintained during UpdateString calls; provides Candidates() for delta.
	SS *spacesaving.SpaceSaving

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
	if s.SS == nil {
		s.SS = spacesaving.NewSpaceSaving(TOPK_SIZE)
	}

	countStore, err := storage.NewFlatVector2DFrom2D(s.Count)
	if err != nil {
		return err
	}

	s.countStore = countStore
	s.Count = countStore.As2D()
	s.bitsPerRow = uint(bits.TrailingZeros(uint(s.Cols)))
	return nil
}

// NewCountSketch creates the float64-counter CountSketch.
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

	countStore, err := storage.NewFlatVector2D(rows, cols)
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
		TopK:       common.NewTopKHeap(TOPK_SIZE),
		SS:         spacesaving.NewSpaceSaving(TOPK_SIZE),
		bitsPerRow: bitsPerRow,
	}, nil
}

// New returns the float64-counter CountSketch with Rust default dimensions.
func New() (*CountSketch, error) {
	return NewCountSketch(RustDefaultRows, RustDefaultCols)
}

// WithDimensions mirrors Rust constructor naming for the float64 variant.
func WithDimensions(rows, cols int) (*CountSketch, error) {
	return NewCountSketch(rows, cols)
}

func (s *CountSketch) RowCount() int { return s.Rows }
func (s *CountSketch) ColCount() int { return s.Cols }

func (s *CountSketch) AsStorage() *storage.FlatVector2D {
	return s.countStore
}

func (s *CountSketch) AsStorageMut() *storage.FlatVector2D {
	return s.countStore
}

// derivePosAndSign computes row index and sign (+1/-1) from base hash.
// Kept for backward-compatibility with existing tests.
func (s *CountSketch) derivePosAndSign(hash uint64, row int) (int, float64) {
	hashed := storage.BuildMatrixHash(hash, s.Rows, s.Cols)
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

func (s *CountSketch) Insert(input *common.SketchInput) {
	if input == nil {
		return
	}
	s.insertWithMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), 1)
}

func (s *CountSketch) InsertMany(input *common.SketchInput, many float64) {
	if input == nil || many == 0 {
		return
	}
	s.insertWithMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), many)
}

func (s *CountSketch) FastInsertWithHashValue(hash uint64) {
	s.InsertWithHash(hash)
}

func (s *CountSketch) FastInsertManyWithHashValue(hash uint64, many float64) {
	s.InsertWithHashAndValue(hash, many)
}

// InsertWithHashAndValue supports weighted updates.
func (s *CountSketch) InsertWithHashAndValue(hash uint64, value float64) {
	hashed := storage.BuildMatrixHash(hash, s.Rows, s.Cols)
	s.insertWithMatrixHash(hashed, value)
}

func (s *CountSketch) insertWithMatrixHash(hashed storage.MatrixHashType, value float64) {
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
		hashed := storage.BuildMatrixHash(hash, s.Rows, s.Cols)
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
			return common.ComputeMedianInlineF64(estimates), nil
		}

		for r := 0; r < s.Rows; r++ {
			c, sign := s.derivePosAndSignFromHashed(hashed, r)
			estimates[r] = count[r][c] * sign
		}
		return common.ComputeMedianInlineF64(estimates), nil

	case common.QuerySum2:
		var l2Stack [16]float64
		l2s := l2Stack[:0]
		if s.Rows > len(l2Stack) {
			l2s = make([]float64, s.Rows)
		} else {
			l2s = l2Stack[:s.Rows]
		}
		copy(l2s, s.L2)
		return math.Sqrt(common.ComputeMedianInlineF64(l2s)), nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (s *CountSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	return s.estimateWithMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols))
}

func (s *CountSketch) FastEstimateWithHash(hash uint64) float64 {
	est, _ := s.QueryWithHash(common.QueryFrequency, hash)
	return est
}

func (s *CountSketch) estimateWithMatrixHash(hashed storage.MatrixHashType) float64 {
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
		return common.ComputeMedianInlineF64(estimates)
	}
	for r := 0; r < s.Rows; r++ {
		c, sign := s.derivePosAndSignFromHashed(hashed, r)
		estimates[r] = count[r][c] * sign
	}
	return common.ComputeMedianInlineF64(estimates)
}

// Reset clears all counters and L2 norms, returning the sketch to its zero state.
func (s *CountSketch) Reset() {
	for i := range s.Count {
		clear(s.Count[i])
	}
	clear(s.L2)
	if s.TopK != nil {
		s.TopK = common.NewTopKHeap(TOPK_SIZE)
	}
	if s.SS != nil {
		s.SS.Reset()
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
		sRow := s.Count[r]
		oRow := o.Count[r]
		for c := 0; c < s.Cols; c++ {
			sRow[c] += oRow[c]
		}
	}

	// 2. Merge TopK: update s.TopK with the other node's heap entries,
	// using the merged CS matrix for accurate counts.
	if s.TopK != nil && o.TopK != nil {
		for _, item := range o.TopK.Heap {
			est, _ := s.QueryWithHash(common.QueryFrequency, common.Hash64([]byte(item.Key)))
			s.TopK.Update(item.Key, int64(est))
		}
	}

	return nil
}

func (s *CountSketch) TypeName() string {
	return "countsketch"
}

// ================= EXTENDED FUNCTIONALITY (TopK Support) =================

// UpdateString updates the sketch matrix and the Space-Saving candidate tracker.
// The SS tracker maintains heavy-hitter candidates at O(log k) cost without
// querying the CS matrix (no CS query on the hot path).
func (s *CountSketch) UpdateString(key string, count float64) {
	hash := common.Hash64([]byte(key))
	s.InsertWithHashAndValue(hash, count)
	if s.SS != nil {
		s.SS.Insert(key, count)
	}
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
