package countminsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// CountMinSketch with single-hash multi-row derivation.
// Hashing MUST be done externally.
type CountMinSketch struct {
	Rows int
	Cols int

	countStore *storage.FlatVector2D
	sumStore   *storage.FlatVector2D
	sum2Store  *storage.FlatVector2D

	Count [][]float64
	Sum   [][]float64
	Sum2  [][]float64

	L1 []float64
	L2 []float64

	bitsPerRow uint
	mask       uint64
}

func hashLayoutForCols(cols int) (uint, uint64) {
	width := 1
	for width < cols {
		width <<= 1
	}
	if width <= 1 {
		return 0, 0
	}
	return uint(bits.TrailingZeros(uint(width))), uint64(width - 1)
}

func (s *CountMinSketch) rehydrateStorage() error {
	if s.Rows <= 0 || s.Cols <= 0 {
		return errors.New("invalid snapshot dimensions")
	}
	if len(s.Count) != s.Rows || len(s.Sum) != s.Rows || len(s.Sum2) != s.Rows {
		return errors.New("invalid snapshot matrix row count")
	}
	for r := 0; r < s.Rows; r++ {
		if len(s.Count[r]) != s.Cols || len(s.Sum[r]) != s.Cols || len(s.Sum2[r]) != s.Cols {
			return errors.New("invalid snapshot matrix col count")
		}
	}
	if len(s.L1) != s.Rows || len(s.L2) != s.Rows {
		return errors.New("invalid snapshot l1/l2 size")
	}

	countStore, err := storage.NewFlatVector2DFrom2D(s.Count)
	if err != nil {
		return err
	}
	sumStore, err := storage.NewFlatVector2DFrom2D(s.Sum)
	if err != nil {
		return err
	}
	sum2Store, err := storage.NewFlatVector2DFrom2D(s.Sum2)
	if err != nil {
		return err
	}

	s.countStore = countStore
	s.sumStore = sumStore
	s.sum2Store = sum2Store
	s.Count = countStore.As2D()
	s.Sum = sumStore.As2D()
	s.Sum2 = sum2Store.As2D()
	s.bitsPerRow, s.mask = hashLayoutForCols(s.Cols)
	return nil
}

/* sketch configurations */
const (
	DefaultRowNum    = 3
	DefaultColNum    = 4096
	QuickStartRowNum = 5
	QuickStartColNum = 2048

	CM_ROW_NO = 5
	CM_COL_NO = 2048
)

// NewCountMinSketch creates the float64-counter Count-Min sketch.
func NewCountMinSketch(row, col int) (*CountMinSketch, error) {
	if row <= 0 || col <= 0 {
		return nil, errors.New("row and col must be positive")
	}

	countStore, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}
	sumStore, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}
	sum2Store, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}

	bitsPerRow, mask := hashLayoutForCols(col)

	return &CountMinSketch{
		Rows:       row,
		Cols:       col,
		countStore: countStore,
		sumStore:   sumStore,
		sum2Store:  sum2Store,
		Count:      countStore.As2D(),
		Sum:        sumStore.As2D(),
		Sum2:       sum2Store.As2D(),
		L1:         make([]float64, row),
		L2:         make([]float64, row),
		bitsPerRow: bitsPerRow,
		mask:       mask,
	}, nil
}

// New returns a float64-counter Count-Min sketch with Rust default dimensions.
func New() (*CountMinSketch, error) {
	return NewCountMinSketch(DefaultRowNum, DefaultColNum)
}

// WithDimensions mirrors Rust constructor naming for the float64 variant.
func WithDimensions(rows, cols int) (*CountMinSketch, error) {
	return NewCountMinSketch(rows, cols)
}

func (s *CountMinSketch) RowCount() int { return s.Rows }
func (s *CountMinSketch) ColCount() int { return s.Cols }

func (s *CountMinSketch) AsStorage() *storage.FlatVector2D {
	return s.countStore
}

func (s *CountMinSketch) AsStorageMut() *storage.FlatVector2D {
	return s.countStore
}

// deriveIndex computes column index from base hash (NO hashing, NO modulo)
func (s *CountMinSketch) deriveIndex(hash uint64, row int) int {
	shift := uint(row) * s.bitsPerRow
	idx := int((hash >> shift) & s.mask)
	if idx >= s.Cols {
		return idx % s.Cols
	}
	return idx
}

// ================= INSERT =================

func (s *CountMinSketch) InsertWithHash(hash uint64) {
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		if c >= s.Cols {
			c %= s.Cols
		}
		shift += s.bitsPerRow

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + 1.0
		countRow[c] = curr
		sumRow[c] += 1.0
		sum2Row[c] += 1.0

		s.L1[r] += 1.0
		s.L2[r] += curr*curr - prev*prev
	}
}

func (s *CountMinSketch) Insert(input *common.SketchInput) {
	if input == nil {
		return
	}
	s.insertMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), 1)
}

func (s *CountMinSketch) InsertMany(input *common.SketchInput, many float64) {
	if input == nil || many == 0 {
		return
	}
	s.insertMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), many)
}

func (s *CountMinSketch) BulkInsert(inputs []*common.SketchInput) {
	for _, input := range inputs {
		s.Insert(input)
	}
}

func (s *CountMinSketch) BulkInsertMany(values []struct {
	Input *common.SketchInput
	Many  float64
}) {
	for _, value := range values {
		s.InsertMany(value.Input, value.Many)
	}
}

// InsertWithHashFast is an optimized alias for benchmark fast-path usage.
func (s *CountMinSketch) InsertWithHashFast(hash uint64) {
	s.InsertWithHash(hash)
}

func (s *CountMinSketch) FastInsertWithHashValue(hash uint64) {
	s.InsertWithHash(hash)
}

func (s *CountMinSketch) FastInsertManyWithHashValue(hash uint64, many float64) {
	if many == 0 {
		return
	}
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		if c >= s.Cols {
			c %= s.Cols
		}
		shift += s.bitsPerRow

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + many
		countRow[c] = curr
		sumRow[c] += many
		sum2Row[c] += many

		s.L1[r] += many
		s.L2[r] += curr*curr - prev*prev
	}
}

func (s *CountMinSketch) insertMatrixHash(hashed storage.MatrixHashType, many float64) {
	for r := 0; r < s.Rows; r++ {
		c := int(hashed.RowHash(r, s.bitsPerRow, s.mask))
		if c >= s.Cols {
			c %= s.Cols
		}

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + many
		countRow[c] = curr
		sumRow[c] += many
		sum2Row[c] += many

		s.L1[r] += many
		s.L2[r] += curr*curr - prev*prev
	}
}

func (s *CountMinSketch) queryFrequencyFast(hash uint64) float64 {
	res := math.MaxFloat64
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		shift += s.bitsPerRow
		v := s.countStore.RowSlice(r)[c]
		if v < res {
			res = v
		}
	}
	return res
}

func (s *CountMinSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	return s.estimateMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols))
}

func (s *CountMinSketch) FastEstimateWithHash(hash uint64) float64 {
	return s.queryFrequencyFast(hash)
}

func (s *CountMinSketch) estimateMatrixHash(hashed storage.MatrixHashType) float64 {
	res := math.MaxFloat64
	for r := 0; r < s.Rows; r++ {
		c := int(hashed.RowHash(r, s.bitsPerRow, s.mask))
		if c >= s.Cols {
			c %= s.Cols
		}
		v := s.countStore.RowSlice(r)[c]
		if v < res {
			res = v
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return res
}

// ================= QUERY =================

func (s *CountMinSketch) QueryWithHash(
	q common.QueryType,
	hash uint64,
) (float64, error) {

	switch q {

	case common.QueryFrequency:
		return s.queryFrequencyFast(hash), nil

	case common.QuerySum:
		res := math.MaxFloat64
		shift := uint(0)
		for r := 0; r < s.Rows; r++ {
			c := int((hash >> shift) & s.mask)
			if c >= s.Cols {
				c %= s.Cols
			}
			shift += s.bitsPerRow
			v := math.Abs(s.sumStore.RowSlice(r)[c])
			if v < res {
				res = v
			}
		}
		return res, nil

	case common.QuerySum2:
		res := math.MaxFloat64
		shift := uint(0)
		for r := 0; r < s.Rows; r++ {
			c := int((hash >> shift) & s.mask)
			if c >= s.Cols {
				c %= s.Cols
			}
			shift += s.bitsPerRow
			v := s.sum2Store.RowSlice(r)[c]
			if v < res {
				res = v
			}
		}
		return res, nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

// ================= METRICS =================

func (s *CountMinSketch) CM_L1() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		if s.L1[i] < res {
			res = s.L1[i]
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return res
}

func (s *CountMinSketch) CM_L2() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		if s.L2[i] < res {
			res = s.L2[i]
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return math.Sqrt(res)
}

func (s *CountMinSketch) TypeName() string {
	return "countmin"
}

// ================= MERGE =================

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
		sCountRow := s.countStore.RowSlice(r)
		sSumRow := s.sumStore.RowSlice(r)
		sSum2Row := s.sum2Store.RowSlice(r)
		oCountRow := o.countStore.RowSlice(r)
		oSumRow := o.sumStore.RowSlice(r)
		oSum2Row := o.sum2Store.RowSlice(r)
		for c := 0; c < s.Cols; c++ {
			sCountRow[c] += oCountRow[c]
			sSumRow[c] += oSumRow[c]
			sSum2Row[c] += oSum2Row[c]
		}
	}
	return nil
}

// SerializeToBytes serializes CountMinSketch into bytes.
func (s *CountMinSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(s)
}

// DeserializeCountMinSketchFromBytes restores CountMinSketch from serialized bytes.
func DeserializeCountMinSketchFromBytes(data []byte) (*CountMinSketch, error) {
	var s CountMinSketch
	if err := common.DecodeFromBytes(data, &s); err != nil {
		return nil, err
	}
	if err := s.rehydrateStorage(); err != nil {
		return nil, err
	}
	return &s, nil
}
