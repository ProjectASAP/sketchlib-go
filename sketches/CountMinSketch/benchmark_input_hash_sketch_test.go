package countminsketch

import (
	"fmt"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/approx-telemetry/sketchlib-go/sketch_framework/hashlayer"
)

func Benchmark_InputToSketch(b *testing.B) {
	cms, err := NewCountMinSketch(3, 2048)
	if err != nil {
		b.Fatalf("NewCountMinSketch error: %v", err)
	}

	hl := hashlayer.New(cms)

	// pre-generate inputs (realistic workload)
	inputs := make([]*common.SketchInput, b.N)
	for i := 0; i < b.N; i++ {
		inputs[i] = common.FromString(fmt.Sprintf("key_%d", i))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		hl.Insert(inputs[i])
	}
}

func Benchmark_InputToMultiSketch(b *testing.B) {
	cms1, _ := NewCountMinSketch(3, 2048)
	cms2, _ := NewCountMinSketch(3, 2048)
	cms3, _ := NewCountMinSketch(3, 2048)

	hl := hashlayer.New(cms1, cms2, cms3)

	inputs := make([]*common.SketchInput, b.N)
	for i := 0; i < b.N; i++ {
		inputs[i] = common.FromString(fmt.Sprintf("user_%d", i))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		hl.Insert(inputs[i])
	}
}

func Benchmark_InputHashOnly(b *testing.B) {
	inputs := make([]*common.SketchInput, b.N)
	for i := 0; i < b.N; i++ {
		inputs[i] = common.FromString(fmt.Sprintf("k_%d", i))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = inputs[i].Hash
	}
}

func Benchmark_SketchOnly(b *testing.B) {
	cms, _ := NewCountMinSketch(3, 2048)

	hashes := make([]uint64, b.N)
	for i := 0; i < b.N; i++ {
		hashes[i] = uint64(i) * 0x9e3779b97f4a7c15
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cms.InsertWithHash(hashes[i])
	}
}
