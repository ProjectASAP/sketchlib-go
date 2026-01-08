package countminsketch

import (
	"fmt"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/approx-telemetry/sketchlib-go/sketch_framework/hashlayer"
)

func Benchmark_EndToEnd_UserAPI(b *testing.B) {
	cms, _ := NewCountMinSketch(3, 2048)
	hl := hashlayer.New(cms)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in := common.FromString(fmt.Sprintf("user_%d", i))
		hl.Insert(in)
	}
}

func Benchmark_EndToEnd_SkewedKeys(b *testing.B) {
	cms, _ := NewCountMinSketch(3, 2048)
	hl := hashlayer.New(cms)

	keys := []string{
		"user_hot_1", "user_hot_2", "user_hot_3",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		in := common.FromString(k)
		hl.Insert(in)
	}
}

func Benchmark_EndToEnd_MultiSketch(b *testing.B) {
	cms1, _ := NewCountMinSketch(3, 2048)
	cms2, _ := NewCountMinSketch(3, 2048)
	cms3, _ := NewCountMinSketch(3, 2048)

	hl := hashlayer.New(cms1, cms2, cms3)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in := common.FromString(fmt.Sprintf("user_%d", i))
		hl.Insert(in)
	}
}

func Benchmark_EndToEnd_Parallel(b *testing.B) {
	cms, _ := NewCountMinSketch(3, 2048)
	hl := hashlayer.New(cms)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			in := common.FromString(fmt.Sprintf("user_%d", i))
			hl.Insert(in)
			i++
		}
	})
}
