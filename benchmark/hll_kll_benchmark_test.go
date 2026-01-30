package benchmark

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	common "github.com/approx-telemetry/sketchlib-go/common"
	hll "github.com/approx-telemetry/sketchlib-go/sketches/HLL"
)

//
// -----------------------
// DATA PREPARATION
// -----------------------
//

func generateFloatData(n int) []float64 {
	data := make([]float64, n)
	for i := 0; i < n; i++ {
		data[i] = float64(i)
	}
	return data
}

func generateHashes(data []float64) []uint64 {
	hashes := make([]uint64, len(data))
	for i, v := range data {
		buf := common.Float64ToBytes(v)
		hashes[i] = common.HashIt(0, buf)
	}
	return hashes
}

//
// -----------------------
// ACCURACY TESTS
// -----------------------
//

func TestHyperLogLogAccuracy(t *testing.T) {
	const tolerance = 0.02

	testCases := []int{10_000, 100_000, 1_000_000}

	for _, n := range testCases {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			h := hll.NewHyperLogLog()

			// Insert n unique items (0 to n-1)
			for i := 0; i < n; i++ {
				h.Insert(float64(i))
			}

			estimate := h.Estimate()
			actual := float64(n)

			diff := math.Abs(float64(estimate) - actual)
			errorRate := diff / actual

			t.Logf("Actual: %d, Estimated: %d, Error: %.4f%%", n, estimate, errorRate*100)

			if errorRate > tolerance {
				t.Errorf("Accuracy failed for N=%d. Got estimate %d. Error %.4f%% exceeds tolerance %.2f%%",
					n, estimate, errorRate*100, tolerance*100)
			}
		})
	}
}

//
// -----------------------
// BENCHMARKS
// -----------------------
//

// Benchmark slow path: hashing + insert.
func BenchmarkHyperLogLogInsertSlowPath(b *testing.B) {
	data := generateFloatData(b.N)

	b.ResetTimer()
	h := hll.NewHyperLogLog()
	for i := 0; i < b.N; i++ {
		h.Insert(data[i])
	}
}

// Benchmark fast path: insert with pre-hashed input.
func BenchmarkHyperLogLogInsertFastPath(b *testing.B) {
	data := generateFloatData(b.N)
	hashes := generateHashes(data)

	b.ResetTimer()
	h := hll.NewHyperLogLog()
	for i := 0; i < b.N; i++ {
		h.InsertWithHash(hashes[i])
	}
}

// Benchmark end-to-end: hashing outside + fast path insert.
func BenchmarkHyperLogLogInsertEndToEnd(b *testing.B) {
	data := generateFloatData(b.N)

	b.ResetTimer()
	h := hll.NewHyperLogLog()
	for i := 0; i < b.N; i++ {
		buf := common.Float64ToBytes(data[i])
		hash := common.HashIt(0, buf)
		h.InsertWithHash(hash)
	}
}

// Benchmark duplicate-heavy workload (important for HLL).
func BenchmarkHyperLogLogDuplicatesFastPath(b *testing.B) {
	h := hll.NewHyperLogLog()

	buf := common.Float64ToBytes(42.0)
	hash := common.HashIt(0, buf)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.InsertWithHash(hash)
	}
}

// Benchmark random input distribution.
func BenchmarkHyperLogLogRandomFastPath(b *testing.B) {
	h := hll.NewHyperLogLog()
	rng := rand.New(rand.NewSource(42))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hash := rng.Uint64()
		h.InsertWithHash(hash)
	}
}
