package common

import "errors"

// DeltaUpdate encodes a single cell-level change emitted by a Worker when its
// local counter reaches threshold τ.
type DeltaUpdate struct {
	Row   int
	Col   int
	Value float64
	Key   []byte // optional: original key bytes for key-based sketches
}

// QueryType enumerates query varieties a sketch may support.
type QueryType int

const (
	// Count-Min Sketch
	QueryFrequency QueryType = iota
	QuerySum
	QuerySum2

	// Other sketches (future)
	QueryCardinality
	QueryQuantile
)

var ErrUnsupportedQuery = errors.New("unsupported query type")

// Sketch is the core contract for all sketches.
// Sketch MUST NOT perform hashing internally.
type Sketch interface {
	InsertWithHash(hash uint64)
	QueryWithHash(q QueryType, hash uint64) (float64, error)
	Merge(other Sketch) error
	TypeName() string
}
