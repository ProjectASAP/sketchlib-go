package common

import (
	"encoding/binary"
)

// SketchInput normalizes input values for all sketches.
type SketchInput struct {
	Bytes []byte
	Hash  uint64
}

// FromString creates SketchInput from string.
func FromString(s string) *SketchInput {
	b := []byte(s)

	cp := make([]byte, len(b))
	copy(cp, b)

	return &SketchInput{
		Bytes: cp,
		Hash:  Hash64(cp),
	}
}

// FromU64 creates SketchInput from uint64.
func FromU64(v uint64) *SketchInput {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)

	cp := make([]byte, 8)
	copy(cp, buf[:])

	return &SketchInput{
		Bytes: cp,
		Hash:  Hash64(cp),
	}
}

// FromBytes creates SketchInput from raw bytes (copied).
func FromBytes(b []byte) *SketchInput {
	cp := make([]byte, len(b))
	copy(cp, b)

	return &SketchInput{
		Bytes: cp,
		Hash:  Hash64(cp),
	}
}

func FromF64(v float64) *SketchInput {
	return FromBytes(Float64ToBytes(v))
}
