package common

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestXxh3RegressionVectors(t *testing.T) {
	key := []byte("projectasap")

	if got := Hash64(key); got != 887548862923853302 {
		t.Fatalf("Hash64() = %d, want %d", got, uint64(887548862923853302))
	}

	if got := HashIt(CanonicalHashSeed, key); got != 8535098769003547387 {
		t.Fatalf("HashIt() = %d, want %d", got, uint64(8535098769003547387))
	}

	got128 := Hash128It(CanonicalHashSeed, key)
	if got128.Lo != 17483029675728789163 || got128.Hi != 10822198452898270743 {
		t.Fatalf(
			"Hash128It() = {Lo:%d Hi:%d}, want {Lo:%d Hi:%d}",
			got128.Lo,
			got128.Hi,
			uint64(17483029675728789163),
			uint64(10822198452898270743),
		)
	}
}

func TestHashItNormalizesSeedIndex(t *testing.T) {
	key := []byte("projectasap")

	if got, want := HashIt(len(seedList)+CanonicalHashSeed, key), HashIt(CanonicalHashSeed, key); got != want {
		t.Fatalf("HashIt() seed normalization mismatch: got %d want %d", got, want)
	}

	got128 := Hash128It(len(seedList)+CanonicalHashSeed, key)
	want128 := Hash128It(CanonicalHashSeed, key)
	if got128 != want128 {
		t.Fatalf("Hash128It() seed normalization mismatch: got %+v want %+v", got128, want128)
	}
}

func TestFloat64Conversions(t *testing.T) {
	cases := []float64{
		0,
		math.Copysign(0, -1),
		1.5,
		-2.75,
		math.Pi,
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
	}

	for idx, val := range cases {
		val := val
		t.Run(fmt.Sprintf("case_%d", idx), func(t *testing.T) {
			expectedBits := math.Float64bits(val)

			stringified := Float64ToString(val)
			if stringified != fmt.Sprint(expectedBits) {
				t.Fatalf("Float64ToString(%v) = %s, want %d", val, stringified, expectedBits)
			}

			asBytes := Float64ToBytes(val)
			if len(asBytes) != 8 {
				t.Fatalf("Float64ToBytes returned %d bytes, want 8", len(asBytes))
			}
			decoded := binary.NativeEndian.Uint64(asBytes)
			if decoded != expectedBits {
				t.Fatalf("Float64ToBytes roundtrip mismatch, got %d want %d", decoded, expectedBits)
			}
		})
	}
}
