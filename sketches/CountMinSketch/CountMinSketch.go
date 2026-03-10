package countminsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// CountMinSketch with single-hash multi-row derivation.
// Hashing MUST be done externally.
type CountMinSketch struct {
	Rows int
	Cols int

	Count [][]float64
	Sum   [][]float64
	Sum2  [][]float64

	L1 []float64
	L2 []float64

	bitsPerRow uint
	mask       uint64
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

	c := make([][]float64, row)
	s := make([][]float64, row)
	s2 := make([][]float64, row)

	for r := 0; r < row; r++ {
		c[r] = make([]float64, col)
		s[r] = make([]float64, col)
		s2[r] = make([]float64, col)
	}

	bitsPerRow := uint(bits.TrailingZeros(uint(col)))

	return &CountMinSketch{
		Rows:       row,
		Cols:       col,
		Count:      c,
		Sum:        s,
		Sum2:       s2,
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
	for r := 0; r < s.Rows; r++ {
		c := s.deriveIndex(hash, r)

		prev := s.Count[r][c]

		s.Count[r][c] += 1
		s.Sum[r][c] += 1
		s.Sum2[r][c] += 1

		s.L1[r] += s.Count[r][c] - prev
		s.L2[r] += s.Count[r][c]*s.Count[r][c] - prev*prev
	}
}

// ================= QUERY =================

func (s *CountMinSketch) QueryWithHash(
	q common.QueryType,
	hash uint64,
) (float64, error) {

	switch q {

	case common.QueryFrequency:
		res := math.MaxFloat64
		for r := 0; r < s.Rows; r++ {
			c := s.deriveIndex(hash, r)
			if s.Count[r][c] < res {
				res = s.Count[r][c]
			}
		}
		return res, nil

	case common.QuerySum:
		res := math.MaxFloat64
		for r := 0; r < s.Rows; r++ {
			c := s.deriveIndex(hash, r)
			v := math.Abs(s.Sum[r][c])
			if v < res {
				res = v
			}
		}
		return res, nil

	case common.QuerySum2:
		res := math.MaxFloat64
		for r := 0; r < s.Rows; r++ {
			c := s.deriveIndex(hash, r)
			if s.Sum2[r][c] < res {
				res = s.Sum2[r][c]
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
		for c := 0; c < s.Cols; c++ {
			s.Count[r][c] += o.Count[r][c]
			s.Sum[r][c] += o.Sum[r][c]
			s.Sum2[r][c] += o.Sum2[r][c]
		}
	}
	return nil
}

type countMinSnapshot struct {
	Rows  int
	Cols  int
	Count [][]float64
	Sum   [][]float64
	Sum2  [][]float64
	L1    []float64
	L2    []float64
}

// SerializeToBytes serializes CountMinSketch into bytes.
func (s *CountMinSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(countMinSnapshot{
		Rows:  s.Rows,
		Cols:  s.Cols,
		Count: s.Count,
		Sum:   s.Sum,
		Sum2:  s.Sum2,
		L1:    s.L1,
		L2:    s.L2,
	})
}

// DeserializeCountMinSketchFromBytes restores CountMinSketch from serialized bytes.
func DeserializeCountMinSketchFromBytes(data []byte) (*CountMinSketch, error) {
	var snap countMinSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.Rows <= 0 || snap.Cols <= 0 {
		return nil, errors.New("invalid snapshot dimensions")
	}
	if snap.Cols&(snap.Cols-1) != 0 {
		return nil, errors.New("invalid snapshot: cols must be power-of-two")
	}
	if len(snap.Count) != snap.Rows || len(snap.Sum) != snap.Rows || len(snap.Sum2) != snap.Rows {
		return nil, errors.New("invalid snapshot matrix row count")
	}
	for r := 0; r < snap.Rows; r++ {
		if len(snap.Count[r]) != snap.Cols || len(snap.Sum[r]) != snap.Cols || len(snap.Sum2[r]) != snap.Cols {
			return nil, errors.New("invalid snapshot matrix col count")
		}
	}
	if len(snap.L1) != snap.Rows || len(snap.L2) != snap.Rows {
		return nil, errors.New("invalid snapshot l1/l2 size")
	}

	return &CountMinSketch{
		Rows:       snap.Rows,
		Cols:       snap.Cols,
		Count:      snap.Count,
		Sum:        snap.Sum,
		Sum2:       snap.Sum2,
		L1:         snap.L1,
		L2:         snap.L2,
		bitsPerRow: uint(bits.TrailingZeros(uint(snap.Cols))),
		mask:       uint64(snap.Cols - 1),
	}, nil
}
