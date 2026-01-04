package hydrasketch

import (
	"flag"
	"os"
	"testing"

	"github.com/golang/glog"
)

func TestMain(m *testing.M) {
	_ = flag.Set("alsologtostderr", "true")
	flag.Parse()
	code := m.Run()
	glog.Flush()
	os.Exit(code)
}

func TestHydraUpdateEstimateSingleKey(t *testing.T) {
	cfg := HydraConfig{
		D: 3,
		W: 8,
		UM: UnivMonConfig{
			Layers: 2,
			Rows:   3,
			Cols:   128,
			TopK:   2,
		},
		UseBigUM: true,
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	const key = "alpha"
	const count = 7
	h.UpdateN(key, count)
	glog.Infof("TestHydraUpdateEstimateSingleKey: updated key=%s count=%d", key, count)

	estimate := h.Estimate(key)
	glog.Infof("TestHydraUpdateEstimateSingleKey: estimate=%d", estimate)
	if estimate != count {
		t.Fatalf("expected estimate %d, got %d", count, estimate)
	}

	if h.Big == nil {
		t.Fatalf("expected Big UnivMon to be initialized")
	}
	global := h.Big.Estimate(key)
	glog.Infof("TestHydraUpdateEstimateSingleKey: global estimate=%d", global)
	if global != count {
		t.Fatalf("expected global estimate %d, got %d", count, global)
	}
}

func TestHydraParallelUpdate(t *testing.T) {
	cfg := HydraConfig{
		D: 4,
		W: 16,
		UM: UnivMonConfig{
			Layers: 3,
			Rows:   3,
			Cols:   256,
			TopK:   4,
		},
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	jobs := []UpdateJob{
		{Key: "alpha", Count: 3},
		{Key: "beta", Count: 5},
		{Key: "alpha", Count: 2},
		{Key: "gamma", Count: 4},
		{Key: "beta", Count: 1},
	}
	glog.Infof("TestHydraParallelUpdate: dispatching %d jobs", len(jobs))

	ParallelUpdate(h, jobs, 3)

	expected := map[string]int{
		"alpha": 5,
		"beta":  6,
		"gamma": 4,
	}

	for key, want := range expected {
		got := h.Estimate(key)
		glog.Infof("TestHydraParallelUpdate: key=%s want=%d got=%d", key, want, got)
		if got != want {
			t.Fatalf("expected estimate %d for key %s, got %d", want, key, got)
		}
	}
}

func TestHydraHashCMBounds(t *testing.T) {
	cfg := HydraConfig{
		D: 5,
		W: 32,
		UM: UnivMonConfig{
			Layers: 1,
			Rows:   1,
			Cols:   32,
		},
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	keys := []string{"alpha", "beta", "gamma", "delta"}
	for _, key := range keys {
		pos := h.hashCM(key)
		glog.Infof("TestHydraHashCMBounds: key=%s positions=%v", key, pos)
		if len(pos) != cfg.D {
			t.Fatalf("expected %d positions, got %d", cfg.D, len(pos))
		}
		for _, p := range pos {
			if p < 0 || p >= cfg.W {
				t.Fatalf("hash position out of bounds: %d (key %s)", p, key)
			}
		}
	}
}
