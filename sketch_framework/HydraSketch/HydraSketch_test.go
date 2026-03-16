package hydrasketch

import (
	"flag"
	"os"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
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
		D:                   3,
		W:                   8,
		CounterType:         HydraCounterUniversal,
		UniversalLayer:      2,
		UniversalRow:        3,
		UniversalCol:        128,
		UniversalTopK:       2,
		EnableGlobalCounter: true,
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	const key = "alpha"
	const count int64 = 7
	v := common.FromString(key)
	h.UpdateValue(key, v, count)

	estimate := int64(h.QueryFrequency([]string{key}, v))
	if estimate != count {
		t.Fatalf("expected estimate %d, got %d", count, estimate)
	}

	if h.bigCounter == nil {
		t.Fatalf("expected global counter to be initialized")
	}
}

func TestHydraParallelUpdate(t *testing.T) {
	cfg := HydraConfig{
		D:             4,
		W:             128,
		CounterType:   HydraCounterCM,
		CounterRows:   3,
		CounterCols:   1024,
		SeedHydra:     123,
		FanoutSubkeys: false,
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
	ParallelUpdate(h, jobs, 3)

	expected := map[string]int64{
		"alpha": 5,
		"beta":  6,
		"gamma": 4,
	}

	for key, want := range expected {
		got := int64(h.QueryFrequency([]string{key}, common.FromString(key)))
		if got < want {
			t.Fatalf("expected estimate >= %d for key %s, got %d", want, key, got)
		}
	}
}

func TestHydraHashCMBounds(t *testing.T) {
	cfg := HydraConfig{D: 5, W: 32}
	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	keys := []string{"alpha", "beta", "gamma", "delta"}
	for _, key := range keys {
		pos := make([]int, cfg.D)
		h.fillPositionsFromSubKey(key, pos)
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

func TestHydraUpdateWithInput(t *testing.T) {
	h, err := NewHydra(HydraConfig{D: 3, W: 16, CounterType: HydraCounterCM, CounterRows: 3, CounterCols: 256})
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	input := common.FromString("beta")
	h.UpdateWithInput(input, 5)

	got := int64(h.QueryFrequency([]string{"beta"}, input))
	if got < 5 {
		t.Fatalf("expected estimate >= 5, got %d", got)
	}
}

func TestHydraTopKDisabled(t *testing.T) {
	h, err := NewHydra(HydraConfig{
		D:                   3,
		W:                   16,
		CounterType:         HydraCounterUniversal,
		UniversalLayer:      2,
		UniversalRow:        3,
		UniversalCol:        128,
		UniversalTopK:       2,
		EnableGlobalCounter: true,
	})
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}
	h.SetTopKEnabled(false)

	h.UpdateValue("gamma", common.FromString("gamma"), 9)
	top := h.TopK(5)
	if len(top) != 0 {
		t.Fatalf("expected empty topk with TopK disabled, got %d", len(top))
	}
}

func TestHydraSerializeRoundTrip(t *testing.T) {
	h, err := NewHydra(HydraConfig{D: 3, W: 16, CounterType: HydraCounterCM, CounterRows: 3, CounterCols: 256})
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	value := common.FromString("delta")
	h.UpdateValue("delta", value, 11)
	before := int64(h.QueryFrequency([]string{"delta"}, value))

	data, err := h.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize failed: %v", err)
	}
	restored, err := DeserializeHydraFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	after := int64(restored.QueryFrequency([]string{"delta"}, value))
	if before != after {
		t.Fatalf("roundtrip mismatch: before=%d after=%d", before, after)
	}
}
