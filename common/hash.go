package common

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/OneOfOne/xxhash"
)

// seedList stores the hash seeds shared across sketches.
// Seeds 0-4 are for Count/CountMin sketches; the last seed is reserved for Count sketch sign hashing.
var seedList = [...]uint64{
	0xcafe3553,
	0xade3415118,
	0x8cc70208,
	0x2f024b2b,
	0x451a3df5,
	0x6a09e667,
}

// Float64ToString returns the IEEE 754 bit pattern of f encoded as a decimal string.
func Float64ToString(f float64) string {
	return fmt.Sprint(math.Float64bits(f))
}

// Float64ToBytes returns the IEEE 754 bit pattern of f encoded in big-endian order.
func Float64ToBytes(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(f))
	return buf
}

// HashIt hashes key using the seed at seedIdx.
// Callers must provide a non-negative seedIdx less than the number of available seeds.
func HashIt(seedIdx int, key []byte) uint64 {
	return xxhash.Checksum64S(key, seedList[seedIdx])
}
