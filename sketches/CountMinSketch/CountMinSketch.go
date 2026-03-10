package countminsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/approx-telemetry/sketchlib-go/common/storage"
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

func (s *CountMinSketch) rehydrateStorage() error {
	if s.Rows <= 0 || s.Cols <= 0 {
		return errors.New("invalid snapshot dimensions")
	}
	if s.Cols&(s.Cols-1) != 0 {
		return errors.New("invalid snapshot: cols must be power-of-two")
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
	s.bitsPerRow = uint(bits.TrailingZeros(uint(s.Cols)))
	s.mask = uint64(s.Cols - 1)
	return nil
}

/* sketch configurations */
const (
	CM_ROW_NO = 5
	CM_COL_NO = 2048
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

	if col&(col-1) != 0 {
		return nil, errors.New("Cols must be power-of-two for fast hashing")
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

	bitsPerRow := uint(bits.TrailingZeros(uint(col)))

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
		mask:       uint64(col - 1),
	}, nil
}

// deriveIndex computes column index from base hash (NO hashing, NO modulo)
func (s *CountMinSketch) deriveIndex(hash uint64, row int) int {
	shift := uint(row) * s.bitsPerRow
	return int((hash >> shift) & s.mask)
}

// ================= INSERT =================

func (s *CountMinSketch) InsertWithHash(hash uint64) {
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
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

// InsertWithHashFast is an optimized alias for benchmark fast-path usage.
func (s *CountMinSketch) InsertWithHashFast(hash uint64) {
	s.InsertWithHash(hash)
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
		sum := 0.0
		row := s.countStore.Row(i)
		for j := 0; j < s.Cols; j++ {
			sum += row[j]
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
		row := s.countStore.Row(i)
		for j := 0; j < s.Cols; j++ {
			sum += row[j] * row[j]
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
