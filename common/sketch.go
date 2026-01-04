package common

import "errors"

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
