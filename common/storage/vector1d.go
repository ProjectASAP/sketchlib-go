package storage

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
)

// Vector1D is a thin wrapper above a slice optimized for sketch workloads.
type Vector1D[T cmp.Ordered] struct {
	data []T
}

// InitVector1D creates an empty vector with reserved capacity.
func InitVector1D[T cmp.Ordered](capacity int) (*Vector1D[T], error) {
	if capacity < 0 {
		return nil, errors.New("capacity must be non-negative")
	}
	return &Vector1D[T]{data: make([]T, 0, capacity)}, nil
}

// FilledVector1D creates a vector with len elements filled with value.
func FilledVector1D[T cmp.Ordered](length int, value T) (*Vector1D[T], error) {
	if length < 0 {
		return nil, errors.New("length must be non-negative")
	}
	data := make([]T, length)
	for i := range data {
		data[i] = value
	}
	return &Vector1D[T]{data: data}, nil
}

// Vector1DFromSlice builds a vector by wrapping an existing slice.
func Vector1DFromSlice[T cmp.Ordered](src []T) *Vector1D[T] {
	return &Vector1D[T]{data: src}
}

// Vector1DFromVec is an alias to match Rust-style naming.
func Vector1DFromVec[T cmp.Ordered](src []T) *Vector1D[T] {
	return Vector1DFromSlice(src)
}

func (v *Vector1D[T]) Fill(value T) {
	for i := range v.data {
		v.data[i] = value
	}
}

func (v *Vector1D[T]) Len() int { return len(v.data) }
func (v *Vector1D[T]) Cap() int { return cap(v.data) }

// GrowZeroed extends the vector to length n, zeroing the newly
// exposed elements and reusing the backing array's capacity when it
// is large enough. Used by sketch stores that are reset+reused across
// windows (object pools): after Clear() drops len to 0 while keeping
// capacity, GrowZeroed re-exposes a zeroed run without allocating.
// A no-op when n <= current length.
func (v *Vector1D[T]) GrowZeroed(n int) {
	if n <= len(v.data) {
		return
	}
	if n <= cap(v.data) {
		old := len(v.data)
		v.data = v.data[:n]
		var zero T
		for i := old; i < n; i++ {
			v.data[i] = zero
		}
		return
	}
	v.data = append(v.data, make([]T, n-len(v.data))...)
}
func (v *Vector1D[T]) IsEmpty() bool {
	return len(v.data) == 0
}

func (v *Vector1D[T]) Get(index int) (*T, error) {
	if index < 0 || index >= len(v.data) {
		return nil, errors.New("index out of range")
	}
	return &v.data[index], nil
}

func (v *Vector1D[T]) GetMut(index int) (*T, error) {
	return v.Get(index)
}

func (v *Vector1D[T]) LastMut() (*T, error) {
	if len(v.data) == 0 {
		return nil, errors.New("vector is empty")
	}
	return &v.data[len(v.data)-1], nil
}

func (v *Vector1D[T]) Push(value T) {
	v.data = append(v.data, value)
}

func (v *Vector1D[T]) Truncate(length int) {
	if length < 0 {
		length = 0
	}
	if length >= len(v.data) {
		return
	}
	v.data = v.data[:length]
}

func (v *Vector1D[T]) Clear() {
	v.data = v.data[:0]
}

func (v *Vector1D[T]) Append(other *Vector1D[T]) {
	if other == nil || len(other.data) == 0 {
		return
	}
	v.data = append(v.data, other.data...)
	other.Clear()
}

func (v *Vector1D[T]) ExtendFromSlice(other []T) {
	v.data = append(v.data, other...)
}

// Insert inserts value at index and shifts tail elements.
func (v *Vector1D[T]) Insert(index int, value T) error {
	if index < 0 || index > len(v.data) {
		return errors.New("index out of range")
	}
	v.data = append(v.data, value)
	copy(v.data[index+1:], v.data[index:])
	v.data[index] = value
	return nil
}

func (v *Vector1D[T]) AsSlice() []T {
	return v.data
}

func (v *Vector1D[T]) AsMutSlice() []T {
	return v.data
}

// Iter returns the current immutable view for range loops.
func (v *Vector1D[T]) Iter() []T {
	return v.data
}

// IterMut returns the mutable view for in-place updates.
func (v *Vector1D[T]) IterMut() []T {
	return v.data
}

func (v *Vector1D[T]) Swap(a, b int) error {
	if a < 0 || a >= len(v.data) || b < 0 || b >= len(v.data) {
		return errors.New("index out of range")
	}
	v.data[a], v.data[b] = v.data[b], v.data[a]
	return nil
}

func (v *Vector1D[T]) UpdateOneCounter(pos int, op func(*T, any), value any) error {
	if pos < 0 || pos >= len(v.data) {
		return errors.New("index out of range")
	}
	if op == nil {
		return errors.New("op must not be nil")
	}
	op(&v.data[pos], value)
	return nil
}

// UpdateOneCounterTyped provides typed callback ergonomics with generic V.
func UpdateOneCounterTyped[T cmp.Ordered, V any](v *Vector1D[T], pos int, op func(*T, V), value V) error {
	if v == nil {
		return errors.New("vector is nil")
	}
	return v.UpdateOneCounter(pos, func(target *T, raw any) {
		op(target, raw.(V))
	}, value)
}

func (v *Vector1D[T]) SortBy(compare func(a, b T) int) {
	slices.SortFunc(v.data, compare)
}

func (v *Vector1D[T]) UpdateIfGreater(index int, value T) error {
	if index < 0 || index >= len(v.data) {
		return errors.New("index out of range")
	}
	if value > v.data[index] {
		v.data[index] = value
	}
	return nil
}

func (v Vector1D[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.data)
}

func (v *Vector1D[T]) UnmarshalJSON(data []byte) error {
	var raw []T
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.data = raw
	return nil
}

func (v *Vector1D[T]) UpdateIfSmaller(index int, value T) error {
	if index < 0 || index >= len(v.data) {
		return errors.New("index out of range")
	}
	if value < v.data[index] {
		v.data[index] = value
	}
	return nil
}
