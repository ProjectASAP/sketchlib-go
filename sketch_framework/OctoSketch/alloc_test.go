package octosketch_test

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	octosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/OctoSketch"
)

func TestWorkerProcess_NoAllocs_CountMin(t *testing.T) {
	s, err := octosketch.NewCountMinOcto(5, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(1 << 20))
	deltaCh := make(chan octosketch.DeltaUpdate, 1024)
	w := octosketch.NewWorker(0, s, tau, deltaCh, nil)
	in := common.FromString("hot-key")

	// Warm-up
	for i := 0; i < 1024; i++ {
		w.Process(in)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		w.Process(in)
	})
	if allocs != 0 {
		t.Fatalf("countmin Process allocs/op = %.2f, want 0", allocs)
	}
}

func TestWorkerProcess_NoAllocs_CountSketch(t *testing.T) {
	s, err := octosketch.NewCountSketchOcto(5, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(1 << 20))
	deltaCh := make(chan octosketch.DeltaUpdate, 1024)
	w := octosketch.NewWorker(0, s, tau, deltaCh, nil)
	in := common.FromString("hot-key")

	for i := 0; i < 1024; i++ {
		w.Process(in)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		w.Process(in)
	})
	if allocs != 0 {
		t.Fatalf("countsketch Process allocs/op = %.2f, want 0", allocs)
	}
}

func TestWorkerProcess_NoAllocs_HLL(t *testing.T) {
	s := octosketch.NewHLLOcto()
	tau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(1))
	deltaCh := make(chan octosketch.DeltaUpdate, 1<<16)
	w := octosketch.NewWorker(0, s, tau, deltaCh, nil)
	in := common.FromString("hot-key")

	for i := 0; i < 1024; i++ {
		w.Process(in)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		w.Process(in)
	})
	if allocs != 0 {
		t.Fatalf("hll Process allocs/op = %.2f, want 0", allocs)
	}
}

func TestWorkerProcess_NoAllocs_DDSketch(t *testing.T) {
	s, err := octosketch.NewDDSketchOcto(0.01)
	if err != nil {
		t.Fatal(err)
	}
	tau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(1 << 20))
	deltaCh := make(chan octosketch.DeltaUpdate, 1024)
	w := octosketch.NewWorker(0, s, tau, deltaCh, nil)
	in := common.FromF64(1234.0)

	// Warm-up initializes any lazy bucket growth.
	for i := 0; i < 1024; i++ {
		w.Process(in)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		w.Process(in)
	})
	if allocs != 0 {
		t.Fatalf("ddsketch Process allocs/op = %.2f, want 0", allocs)
	}
}

