package common

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

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
			decoded := binary.BigEndian.Uint64(asBytes)
			if decoded != expectedBits {
				t.Fatalf("Float64ToBytes roundtrip mismatch, got %d want %d", decoded, expectedBits)
			}
		})
	}
}
