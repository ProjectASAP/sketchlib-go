package storage

import (
	"errors"
	"fmt"
	"slices"

	"github.com/ProjectASAP/sketchlib-go/common"
)

type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Matrix2D is the storage interface used by matrix sketches.
type Matrix2D[T any] interface {
	Rows() int
	Cols() int
	At(row, col int) T
	Set(row, col int, value T)
	Add(row, col int, delta T)
	Row(row int) []T
	RowSlice(row int) []T
	As2D() [][]T
}

// Vector2D is a thin wrapper over flat 1D data for a rows x cols matrix.
type Vector2D[T any] struct {
	data []T
	view [][]T

	rows int
	cols int

	maskBits uint
	mask     uint64
	hashMode MatrixHashMode
}

// InitVector2D creates a zero-initialized matrix with rows*cols capacity.
func InitVector2D[T any](rows, cols int) (*Vector2D[T], error) {
	if rows <= 0 || cols <= 0 {
		return nil, errors.New("rows and cols must be positive")
	}

	v := &Vector2D[T]{
		data:     make([]T, rows*cols),
		view:     make([][]T, rows),
		rows:     rows,
		cols:     cols,
		maskBits: maskBitsForCols(cols),
		mask:     uint64(cols - 1),
		hashMode: HashModeForMatrix(rows, cols),
	}
	for r := 0; r < rows; r++ {
		start := r * cols
		v.view[r] = v.data[start : start+cols]
	}
	return v, nil
}

// Vector2DFromFn builds a matrix from a generator function.
func Vector2DFromFn[T any](rows, cols int, fn func(row, col int) T) (*Vector2D[T], error) {
	v, err := InitVector2D[T](rows, cols)
	if err != nil {
		return nil, err
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			v.Set(r, c, fn(r, c))
		}
	}
	return v, nil
}

// Vector2DFrom2D constructs a matrix by copying from 2D input.
func Vector2DFrom2D[T any](src [][]T) (*Vector2D[T], error) {
	if len(src) == 0 || len(src[0]) == 0 {
		return nil, errors.New("source matrix must be non-empty")
	}
	rows := len(src)
	cols := len(src[0])

	v, err := InitVector2D[T](rows, cols)
	if err != nil {
		return nil, err
	}
	for r := 0; r < rows; r++ {
		if len(src[r]) != cols {
			return nil, errors.New("ragged matrix is not supported")
		}
		copy(v.view[r], src[r])
	}
	return v, nil
}

func (v *Vector2D[T]) Rows() int { return v.rows }
func (v *Vector2D[T]) Cols() int { return v.cols }
func (v *Vector2D[T]) MaskBits() uint {
	return v.maskBits
}
func (v *Vector2D[T]) Mask() uint64 {
	return v.mask
}
func (v *Vector2D[T]) HashMode() MatrixHashMode {
	return v.hashMode
}

// Index returns flat index into underlying memory.
func (v *Vector2D[T]) Index(row, col int) int {
	return row*v.cols + col
}

func (v *Vector2D[T]) At(row, col int) T {
	return v.data[v.Index(row, col)]
}

func (v *Vector2D[T]) Set(row, col int, value T) {
	v.data[v.Index(row, col)] = value
}

func (v *Vector2D[T]) Add(row, col int, delta T) {
	idx := v.Index(row, col)
	v.data[idx] = addValue(v.data[idx], delta)
}

func (v *Vector2D[T]) Row(row int) []T {
	return v.view[row]
}

// RowSlice returns a zero-allocation row slice view.
func (v *Vector2D[T]) RowSlice(row int) []T {
	start := row * v.cols
	return v.data[start : start+v.cols]
}

func (v *Vector2D[T]) As2D() [][]T {
	return v.view
}

// FastInsert executes high-order fast update path where hash mapping logic is inlined in closure.
func (v *Vector2D[T]) FastInsert(
	value T,
	colFn func(row int, maskBits uint, mask uint64) int,
	updateFn func(current T, delta T) T,
) {
	if updateFn == nil {
		updateFn = func(current T, delta T) T { return addValue(current, delta) }
	}
	for r := 0; r < v.rows; r++ {
		c := colFn(r, v.maskBits, v.mask)
		idx := v.Index(r, c)
		v.data[idx] = updateFn(v.data[idx], value)
	}
}

// FastQueryMin executes fast min query path with inline hash mapping logic.
func (v *Vector2D[T]) FastQueryMin(
	colFn func(row int, maskBits uint, mask uint64) int,
) T {
	if v.rows == 0 {
		var zero T
		return zero
	}
	minVal := v.At(0, colFn(0, v.maskBits, v.mask))
	for r := 1; r < v.rows; r++ {
		c := colFn(r, v.maskBits, v.mask)
		val := v.At(r, c)
		if lessValue(val, minVal) {
			minVal = val
		}
	}
	return minVal
}

// FastQueryMedian executes fast median query path with inline hash mapping logic.
func (v *Vector2D[T]) FastQueryMedian(
	colFn func(row int, maskBits uint, mask uint64) int,
	projectFn func(row int, value T) T,
) T {
	if v.rows == 0 {
		var zero T
		return zero
	}
	if projectFn == nil {
		projectFn = func(_ int, value T) T { return value }
	}
	values := make([]T, v.rows)
	for r := 0; r < v.rows; r++ {
		c := colFn(r, v.maskBits, v.mask)
		values[r] = projectFn(r, v.At(r, c))
	}
	slices.SortFunc(values, func(a, b T) int {
		if lessValue(a, b) {
			return -1
		}
		if lessValue(b, a) {
			return 1
		}
		return 0
	})
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return avgValue(values[mid-1], values[mid])
}

// FastQueryAggregate executes fast query path with custom aggregation logic.
func (v *Vector2D[T]) FastQueryAggregate(
	colFn func(row int, maskBits uint, mask uint64) int,
	init T,
	aggFn func(acc T, row int, value T) T,
) T {
	acc := init
	for r := 0; r < v.rows; r++ {
		c := colFn(r, v.maskBits, v.mask)
		acc = aggFn(acc, r, v.At(r, c))
	}
	return acc
}

type vector2DSnapshot[T any] struct {
	Data     []T
	Rows     int
	Cols     int
	MaskBits uint
	Mask     uint64
	HashMode MatrixHashMode
}

func (v *Vector2D[T]) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(vector2DSnapshot[T]{
		Data:     v.data,
		Rows:     v.rows,
		Cols:     v.cols,
		MaskBits: v.maskBits,
		Mask:     v.mask,
		HashMode: v.hashMode,
	})
}

func DeserializeVector2DFromBytes[T any](data []byte) (*Vector2D[T], error) {
	var snap vector2DSnapshot[T]
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.Rows <= 0 || snap.Cols <= 0 {
		return nil, errors.New("invalid dimensions")
	}
	if len(snap.Data) != snap.Rows*snap.Cols {
		return nil, errors.New("invalid data size")
	}

	v, err := InitVector2D[T](snap.Rows, snap.Cols)
	if err != nil {
		return nil, err
	}
	copy(v.data, snap.Data)
	v.maskBits = snap.MaskBits
	v.mask = snap.Mask
	v.hashMode = snap.HashMode
	return v, nil
}

// Compatibility aliases/wrappers for existing float64 sketch code paths.
type FlatVector2D = Vector2D[float64]

func NewFlatVector2D(rows, cols int) (*FlatVector2D, error) {
	return InitVector2D[float64](rows, cols)
}

func NewFlatVector2DFrom2D(src [][]float64) (*FlatVector2D, error) {
	return Vector2DFrom2D[float64](src)
}

// SortBy allows custom comparators (Rust-like sort_by).
func (v *Vector2D[T]) SortBy(compare func(a, b T) int) {
	slices.SortFunc(v.data, compare)
}

func addValue[T any](a, b T) T {
	switch av := any(a).(type) {
	case int:
		return any(av + any(b).(int)).(T)
	case int8:
		return any(av + any(b).(int8)).(T)
	case int16:
		return any(av + any(b).(int16)).(T)
	case int32:
		return any(av + any(b).(int32)).(T)
	case int64:
		return any(av + any(b).(int64)).(T)
	case uint:
		return any(av + any(b).(uint)).(T)
	case uint8:
		return any(av + any(b).(uint8)).(T)
	case uint16:
		return any(av + any(b).(uint16)).(T)
	case uint32:
		return any(av + any(b).(uint32)).(T)
	case uint64:
		return any(av + any(b).(uint64)).(T)
	case float32:
		return any(av + any(b).(float32)).(T)
	case float64:
		return any(av + any(b).(float64)).(T)
	default:
		panic(fmt.Sprintf("Vector2D: Add unsupported for type %T", a))
	}
}

func lessValue[T any](a, b T) bool {
	switch av := any(a).(type) {
	case int:
		return av < any(b).(int)
	case int8:
		return av < any(b).(int8)
	case int16:
		return av < any(b).(int16)
	case int32:
		return av < any(b).(int32)
	case int64:
		return av < any(b).(int64)
	case uint:
		return av < any(b).(uint)
	case uint8:
		return av < any(b).(uint8)
	case uint16:
		return av < any(b).(uint16)
	case uint32:
		return av < any(b).(uint32)
	case uint64:
		return av < any(b).(uint64)
	case float32:
		return av < any(b).(float32)
	case float64:
		return av < any(b).(float64)
	default:
		panic(fmt.Sprintf("Vector2D: comparison unsupported for type %T", a))
	}
}

func avgValue[T any](a, b T) T {
	switch av := any(a).(type) {
	case int:
		return any((av + any(b).(int)) / 2).(T)
	case int8:
		return any((av + any(b).(int8)) / 2).(T)
	case int16:
		return any((av + any(b).(int16)) / 2).(T)
	case int32:
		return any((av + any(b).(int32)) / 2).(T)
	case int64:
		return any((av + any(b).(int64)) / 2).(T)
	case uint:
		return any((av + any(b).(uint)) / 2).(T)
	case uint8:
		return any((av + any(b).(uint8)) / 2).(T)
	case uint16:
		return any((av + any(b).(uint16)) / 2).(T)
	case uint32:
		return any((av + any(b).(uint32)) / 2).(T)
	case uint64:
		return any((av + any(b).(uint64)) / 2).(T)
	case float32:
		return any((av + any(b).(float32)) / 2.0).(T)
	case float64:
		return any((av + any(b).(float64)) / 2.0).(T)
	default:
		panic(fmt.Sprintf("Vector2D: median unsupported for type %T", a))
	}
}
