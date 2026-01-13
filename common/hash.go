package common

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/cespare/xxhash/v2"
)

// seedList stores the hash seeds shared across sketches.
// Seeds 0-4 are for Count/CountMin sketches;
// the last seed is reserved for CountSketch sign hashing.
var seedList = [...]uint64{
	0xcafe3553,
	0xade3415118,
	0x8cc70208,
	0x2f024b2b,
	0x451a3df5,
	0x6a09e667,
}

// Hash64 returns unseeded xxhash of key.
// Used as base hash and cached in SketchInput.
func Hash64(key []byte) uint64 {
	return xxhash.Sum64(key)
}

// HashIt hashes key using the seed at seedIdx.
// Hash = xxhash( seed_bytes || key )
func HashIt(seedIdx int, key []byte) uint64 {
	seed := seedList[seedIdx]

	h := xxhash.New()

	var seedBuf [8]byte
	binary.LittleEndian.PutUint64(seedBuf[:], seed)

	h.Write(seedBuf[:])
	h.Write(key)

	return h.Sum64()
}

// Float64ToString returns IEEE754 bit pattern as decimal string.
func Float64ToString(f float64) string {
	return fmt.Sprint(math.Float64bits(f))
}

// Float64ToBytes returns IEEE754 bit pattern in big-endian.
func Float64ToBytes(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(f))
	return buf
}

// DeriveIndex derives column index for a given row
// width MUST be power-of-two
func DeriveIndex(hash uint64, row int, width uint32) uint32 {
	// kebutuhan bit per row
	shift := uint(row * 11) // cukup untuk width ≤ 2048
	mask := uint64(width - 1)
	return uint32((hash >> shift) & mask)
}

// DeriveSign derives +1 / -1 for CountSketch
func DeriveSign(hash uint64, row int) int64 {
	bit := (hash >> uint(63-row)) & 1
	if bit == 0 {
		return -1
	}
	return 1
}
