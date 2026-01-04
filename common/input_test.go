package common

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/cespare/xxhash/v2"
)

func TestFloat64InputBaseHash(t *testing.T) {
	val := 3.141592653589793

	b := Float64ToBytes(val)
	in := FromBytes(b)

	if len(in.Bytes) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(in.Bytes))
	}

	expected := Hash64(b)
	if in.Hash != expected {
		t.Fatalf("base hash mismatch: got %d, want %d", in.Hash, expected)
	}
}

func TestHashItDeterministicPerSeed(t *testing.T) {
	key := []byte("hello")

	h1 := HashIt(0, key)
	h2 := HashIt(0, key)

	if h1 != h2 {
		t.Fatal("HashIt must be deterministic for same seed and key")
	}
}

func TestHashItDifferentSeeds(t *testing.T) {
	key := []byte("hello")

	h0 := HashIt(0, key)
	h1 := HashIt(1, key)

	if h0 == h1 {
		t.Fatal("different seeds should produce different hashes")
	}
}

func TestHashItDifferentKeys(t *testing.T) {
	h1 := HashIt(0, []byte("foo"))
	h2 := HashIt(0, []byte("bar"))

	if h1 == h2 {
		t.Fatal("different keys should not hash to same value")
	}
}

func TestHashItSeedPrefixContract(t *testing.T) {
	key := []byte("contract-test")
	seedIdx := 0

	seed := seedList[seedIdx]

	h := xxhash.New()

	var seedBuf [8]byte
	binary.LittleEndian.PutUint64(seedBuf[:], seed)

	h.Write(seedBuf[:])
	h.Write(key)

	expected := h.Sum64()
	got := HashIt(seedIdx, key)

	if got != expected {
		t.Fatalf("HashIt contract broken: got %d, want %d", got, expected)
	}
}

func TestFloat64InputSeededHash(t *testing.T) {
	in := FromF64(0.125)

	h0 := HashIt(0, in.Bytes)
	h1 := HashIt(1, in.Bytes)

	if h0 == h1 {
		t.Fatal("seeded hashes should differ for same input")
	}
}

func TestInputCopySafety(t *testing.T) {
	src := []byte{1, 2, 3, 4}
	in := FromBytes(src)

	src[0] = 9

	if bytes.Equal(src, in.Bytes) {
		t.Fatal("input bytes must be copied")
	}
}
