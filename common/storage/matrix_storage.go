package storage

import (
	"errors"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// MatrixHashMode defines how per-row hash representation is stored.
type MatrixHashMode int

const (
	MatrixHashPacked64 MatrixHashMode = iota
	MatrixHashPacked128
	MatrixHashRows
)

// MatrixHashType stores fast-path hash representation for matrix sketches.
// It is designed to support "hash once, derive all row indexes/signs".
type MatrixHashType struct {
	mode MatrixHashMode

	// Packed64 mode.
	packed64 uint64

	// Packed128 mode (hi: most-significant 64 bits, lo: least-significant 64 bits).
	packed128Hi uint64
	packed128Lo uint64

	// Rows mode (one 64-bit hash per row).
	rows []uint64
}

func (m MatrixHashType) Mode() MatrixHashMode { return m.mode }

func (m MatrixHashType) Lower64() uint64 {
	switch m.mode {
	case MatrixHashPacked64:
		return m.packed64
	case MatrixHashPacked128:
		return m.packed128Lo
	case MatrixHashRows:
		if len(m.rows) == 0 {
			return 0
		}
		return m.rows[0]
	default:
		return 0
	}
}

// RowHash returns per-row hash bits masked for matrix column selection.
func (m MatrixHashType) RowHash(row int, maskBits uint, mask uint64) uint64 {
	switch m.mode {
	case MatrixHashPacked64:
		shift := maskBits * uint(row)
		return (m.packed64 >> shift) & mask

	case MatrixHashPacked128:
		shift := maskBits * uint(row)
		shifted := shiftRight128(m.packed128Hi, m.packed128Lo, shift)
		return shifted & mask

	case MatrixHashRows:
		if row < 0 || row >= len(m.rows) {
			return 0
		}
		return m.rows[row] & mask
	default:
		return 0
	}
}

// SignForRow derives +1/-1 sign bit for CountSketch-like updates.
func (m MatrixHashType) SignForRow(row int) int64 {
	var bit uint64
	switch m.mode {
	case MatrixHashPacked64:
		if row < 0 || row > 63 {
			return 1
		}
		bit = (m.packed64 >> uint(63-row)) & 1

	case MatrixHashPacked128:
		if row < 0 || row > 127 {
			return 1
		}
		shift := uint(127 - row)
		bit = bitAt128(m.packed128Hi, m.packed128Lo, shift)

	case MatrixHashRows:
		if row < 0 || row >= len(m.rows) {
			return 1
		}
		bit = (m.rows[row] >> 63) & 1
	}
	if bit == 0 {
		return -1
	}
	return 1
}

func maskBitsForCols(cols int) uint {
	if cols <= 1 {
		return 1
	}
	u := uint(cols - 1)
	var bits uint
	for u > 0 {
		bits++
		u >>= 1
	}
	return bits
}

// HashModeForMatrix chooses representation mode based on rows/cols requirements.
func HashModeForMatrix(rows, cols int) MatrixHashMode {
	maskBits := maskBitsForCols(cols)
	bitsPerRow := maskBits + 1 // +1 for sign bit
	bitsRequired := uint(rows) * bitsPerRow

	if bitsRequired <= 64 {
		return MatrixHashPacked64
	}
	if bitsRequired <= 128 {
		return MatrixHashPacked128
	}
	return MatrixHashRows
}

// BuildMatrixHash converts a base 64-bit hash into per-matrix hash representation.
// This keeps API hash-once while still supporting large matrix dimensions.
func BuildMatrixHash(baseHash uint64, rows, cols int) MatrixHashType {
	mode := HashModeForMatrix(rows, cols)
	switch mode {
	case MatrixHashPacked64:
		return MatrixHashType{
			mode:     MatrixHashPacked64,
			packed64: baseHash,
		}

	case MatrixHashPacked128:
		hi := mix64(baseHash ^ 0x9e3779b97f4a7c15)
		return MatrixHashType{
			mode:        MatrixHashPacked128,
			packed128Hi: hi,
			packed128Lo: baseHash,
			rows:        nil,
			packed64:    0,
		}

	default:
		seq := make([]uint64, rows)
		x := baseHash
		for i := 0; i < rows; i++ {
			x = mix64(x + uint64(i)*0x9e3779b97f4a7c15)
			seq[i] = x
		}
		return MatrixHashType{
			mode: MatrixHashRows,
			rows: seq,
		}
	}
}

// MatrixStorage defines matrix sketch storage operations.
type MatrixStorage interface {
	Rows() int
	Cols() int

	UpdateOneCounter(row, col int, delta float64)
	QueryOneCounter(row, col int) float64

	// FastInsert updates counters for all rows using one hash representation.
	FastInsert(hashed MatrixHashType, delta float64)

	// FastQueryMin returns min counter across derived row positions.
	FastQueryMin(hashed MatrixHashType) float64

	// FastQueryMedianSigned is useful for CountSketch:
	// value[row,col] * sign(row), then median across rows.
	FastQueryMedianSigned(hashed MatrixHashType) float64

	// HashForMatrix derives per-row hash representation from base hash.
	HashForMatrix(baseHash uint64) MatrixHashType
}

// DenseMatrixStorage is a flattened 1D matrix backend with fast hash-aware operations.
type DenseMatrixStorage struct {
	matrix *FlatVector2D

	maskBits uint
	mask     uint64
}

func NewDenseMatrixStorage(rows, cols int) (*DenseMatrixStorage, error) {
	if rows <= 0 || cols <= 0 {
		return nil, errors.New("rows and cols must be positive")
	}
	if cols&(cols-1) != 0 {
		return nil, errors.New("cols must be power-of-two")
	}
	m, err := NewFlatVector2D(rows, cols)
	if err != nil {
		return nil, err
	}
	return &DenseMatrixStorage{
		matrix:   m,
		maskBits: maskBitsForCols(cols),
		mask:     uint64(cols - 1),
	}, nil
}

func (d *DenseMatrixStorage) Rows() int { return d.matrix.Rows() }
func (d *DenseMatrixStorage) Cols() int { return d.matrix.Cols() }
func (d *DenseMatrixStorage) As2D() [][]float64 {
	return d.matrix.As2D()
}

func (d *DenseMatrixStorage) UpdateOneCounter(row, col int, delta float64) {
	d.matrix.Add(row, col, delta)
}

func (d *DenseMatrixStorage) QueryOneCounter(row, col int) float64 {
	return d.matrix.At(row, col)
}

func (d *DenseMatrixStorage) HashForMatrix(baseHash uint64) MatrixHashType {
	return BuildMatrixHash(baseHash, d.Rows(), d.Cols())
}

func (d *DenseMatrixStorage) FastInsert(hashed MatrixHashType, delta float64) {
	for r := 0; r < d.Rows(); r++ {
		col := int(hashed.RowHash(r, d.maskBits, d.mask))
		d.matrix.Add(r, col, delta)
	}
}

func (d *DenseMatrixStorage) FastQueryMin(hashed MatrixHashType) float64 {
	minVal := math.MaxFloat64
	for r := 0; r < d.Rows(); r++ {
		col := int(hashed.RowHash(r, d.maskBits, d.mask))
		v := d.matrix.At(r, col)
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func (d *DenseMatrixStorage) FastQueryMedianSigned(hashed MatrixHashType) float64 {
	var valuesStack [16]float64
	values := valuesStack[:0]
	if d.Rows() > len(valuesStack) {
		values = make([]float64, d.Rows())
	} else {
		values = valuesStack[:d.Rows()]
	}
	for r := 0; r < d.Rows(); r++ {
		col := int(hashed.RowHash(r, d.maskBits, d.mask))
		sign := float64(hashed.SignForRow(r))
		values[r] = d.matrix.At(r, col) * sign
	}
	return common.ComputeMedianInlineF64(values)
}

func shiftRight128(hi, lo uint64, shift uint) uint64 {
	if shift == 0 {
		return lo
	}
	if shift < 64 {
		return (lo >> shift) | (hi << (64 - shift))
	}
	if shift < 128 {
		return hi >> (shift - 64)
	}
	return 0
}

func bitAt128(hi, lo uint64, shift uint) uint64 {
	if shift < 64 {
		return (lo >> shift) & 1
	}
	if shift < 128 {
		return (hi >> (shift - 64)) & 1
	}
	return 1
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
