package storage

import "errors"

// Vector3D is a thin wrapper over flat 1D data for a layer x row x col tensor.
type Vector3D[T any] struct {
	data  []T
	layer int
	row   int
	col   int
}

// InitVector3D creates a zero-initialized 3D vector.
func InitVector3D[T any](layer, row, col int) (*Vector3D[T], error) {
	if layer <= 0 || row <= 0 || col <= 0 {
		return nil, errors.New("layer, row, and col must be positive")
	}
	return &Vector3D[T]{
		data:  make([]T, layer*row*col),
		layer: layer,
		row:   row,
		col:   col,
	}, nil
}

func (v *Vector3D[T]) Layers() int { return v.layer }
func (v *Vector3D[T]) Rows() int   { return v.row }
func (v *Vector3D[T]) Cols() int   { return v.col }

func (v *Vector3D[T]) index(layer, row, col int) int {
	return (layer*v.row+row)*v.col + col
}

func (v *Vector3D[T]) At(layer, row, col int) T {
	return v.data[v.index(layer, row, col)]
}

func (v *Vector3D[T]) Set(layer, row, col int, value T) {
	v.data[v.index(layer, row, col)] = value
}

func (v *Vector3D[T]) AsSlice() []T {
	return v.data
}
