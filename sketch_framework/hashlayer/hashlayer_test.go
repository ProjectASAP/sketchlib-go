package hashlayer

import (
	"math/rand"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// MockSketch is a lightweight dummy implementation for benchmarking
// so we only measure dispatching overhead, not the sketch logic.
type MockSketch struct {
	counter int64
}

func (m *MockSketch) InsertWithHash(hash uint64) {
	// Simulate lightweight operation
	m.counter += int64(hash & 1)
}

func (m *MockSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	return float64(m.counter), nil
}

func (m *MockSketch) Merge(other common.Sketch) error {
	return nil
}

func (m *MockSketch) TypeName() string {
	return "MockSketch"
}

// Benchmark data preparation
const SAMPLE_COUNT = 10000

func buildInputs() []*common.SketchInput {
	inputs := make([]*common.SketchInput, SAMPLE_COUNT)
	for i := 0; i < SAMPLE_COUNT; i++ {
		// Using FromU64 to match the Rust benchmark (SketchInput::U64)
		inputs[i] = common.FromU64(rand.Uint64())
	}
	return inputs
}

// Benchmark 1: Insert into 3 sketches separately (Manual Loop)
func BenchmarkSeparateInsert_ThreeSketches(b *testing.B) {
	// Setup 3 sketches
	s1 := &MockSketch{}
	s2 := &MockSketch{}
	s3 := &MockSketch{}

	inputs := buildInputs()
	inputsLen := len(inputs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := inputs[i%inputsLen]

		// Manual insert into each sketch
		// (Mimics cm.insert(key); count.insert(key); hll.insert(key))
		s1.InsertWithHash(input.Hash)
		s2.InsertWithHash(input.Hash)
		s3.InsertWithHash(input.Hash)
	}
}

// Benchmark 2: Insert using HashLayer
func BenchmarkHashLayerInsert_ThreeSketches(b *testing.B) {
	// Setup HashLayer with 3 sketches
	s1 := &MockSketch{}
	s2 := &MockSketch{}
	s3 := &MockSketch{}
	hl := New(s1, s2, s3)

	inputs := buildInputs()
	inputsLen := len(inputs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := inputs[i%inputsLen]

		// Single insert via HashLayer
		// HashLayer will perform internal looping
		hl.Insert(input)
	}
}
