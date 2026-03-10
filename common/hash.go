package common

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/zeebo/xxh3"
)

// seedList stores the hash seeds shared across sketches.
// This mirrors sketchlib-rust's seed table.
var seedList = [...]uint64{
	0xcafe3553,
	0xade3415118,
	0x8cc70208,
	0x2f024b2b,
	0x451a3df5,
	0x6a09e667,
	0xbb67ae85,
	0x3c6ef372,
	0xa54ff53a,
	0x510e527f,
	0x9b05688c,
	0x1f83d9ab,
	0x5be0cd19,
	0xcbbb9d5d,
	0x629a292a,
	0x9159015a,
	0x152fecd8,
	0x67332667,
	0x8eb44a87,
	0xdb0c2e0d,
}

const CanonicalHashSeed = 5

type Hash128 struct {
	Lo uint64
	Hi uint64
}

func normalizedSeedIdx(seedIdx int) int {
	n := len(seedList)
	seedIdx %= n
	if seedIdx < 0 {
		seedIdx += n
	}
	return seedIdx
}

// Hash64 returns the Rust-compatible default seeded hash used by the fast-path
// matrix sketches.
func Hash64(key []byte) uint64 {
	return HashIt(0, key)
}

// HashIt hashes key with the Rust-compatible seeded XXH3 64-bit function.
func HashIt(seedIdx int, key []byte) uint64 {
	return xxh3.HashSeed(key, seedList[normalizedSeedIdx(seedIdx)])
}

// Hash128It hashes key with the Rust-compatible seeded XXH3 128-bit function.
func Hash128It(seedIdx int, key []byte) Hash128 {
	hash := xxh3.Hash128Seed(key, seedList[normalizedSeedIdx(seedIdx)])
	return Hash128{
		Lo: hash.Lo,
		Hi: hash.Hi,
	}
}

// Float64ToString returns IEEE754 bit pattern as decimal string.
func Float64ToString(f float64) string {
	return fmt.Sprint(math.Float64bits(f))
}

// Float64ToBytes returns IEEE754 bit pattern in native-endian, matching Rust's
// to_ne_bytes on the current platform.
func Float64ToBytes(f float64) []byte {
	buf := make([]byte, 8)
	binary.NativeEndian.PutUint64(buf, math.Float64bits(f))
	return buf
}

func maskBitsForWidth(width uint32) uint {
	if width <= 1 {
		return 1
	}
	u := width - 1
	var bits uint
	for u > 0 {
		bits++
		u >>= 1
	}
	return bits
}

// DeriveIndex derives the row-local column index using the same bit slicing
// convention used by the Rust fast-path matrix hashes.
func DeriveIndex(hash uint64, row int, width uint32) uint32 {
	shift := uint(row) * maskBitsForWidth(width)
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
