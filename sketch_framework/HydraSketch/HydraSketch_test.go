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
		D:            3,
		W:            8,
		UnivMonLayer: 2,
		UnivMonRow:   3,
		UnivMonCol:   128,
		UnivMonTopK:  2,
		UseBigUM:     true,
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	const key = "alpha"
	const count int64 = 7
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

	// FIX: Use FromString(key).Hash to match the hash used during Update
	hash := common.FromString(key).Hash
	globalFloat, err := h.Big.QueryWithHash(common.QuerySum, hash)
	if err != nil {
		t.Fatalf("error querying global sketch: %v", err)
	}

	global := int64(globalFloat)
	glog.Infof("TestHydraUpdateEstimateSingleKey: global estimate=%d", global)

	if global != count {
		t.Fatalf("expected global estimate %d, got %d", count, global)
	}
}

func TestHydraParallelUpdate(t *testing.T) {
	cfg := HydraConfig{
		D:            4,
		W:            16,
		UnivMonLayer: 3,
		UnivMonRow:   3,
		UnivMonCol:   256,
		UnivMonTopK:  4,
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

	expected := map[string]int64{
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
		D:            5,
		W:            32,
		UnivMonLayer: 1,
		UnivMonRow:   1,
		UnivMonCol:   32,
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

func TestHydraUpdateWithInput(t *testing.T) {
	cfg := HydraConfig{
		D:            3,
		W:            8,
		UnivMonLayer: 2,
		UnivMonRow:   3,
		UnivMonCol:   128,
		UnivMonTopK:  2,
		UseBigUM:     true,
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}

	input := common.FromString("beta")
	h.UpdateWithInput(input, 5)

	got := h.Estimate("beta")
	if got != 5 {
		t.Fatalf("expected estimate 5, got %d", got)
	}
}

func TestHydraUpdateWithHash_NoTopKMode(t *testing.T) {
	cfg := HydraConfig{
		D:            3,
		W:            8,
		UnivMonLayer: 2,
		UnivMonRow:   3,
		UnivMonCol:   128,
		UnivMonTopK:  2,
		UseBigUM:     true,
	}

	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}
	h.SetTopKEnabled(false)

	hash := common.FromString("gamma").Hash
	h.UpdateWithHash(hash, 9)
	got := h.EstimateWithHash(hash)
	if got != 9 {
		t.Fatalf("expected estimate 9, got %d", got)
	}

	// With TopK disabled, heavy-hitter query should not accumulate entries.
	top := h.TopK(5)
	if len(top) != 0 {
		t.Fatalf("expected empty topk with TopK disabled, got %d", len(top))
	}
}

func TestHydraSerializeRoundTrip(t *testing.T) {
	cfg := HydraConfig{
		D:            3,
		W:            8,
		UnivMonLayer: 2,
		UnivMonRow:   3,
		UnivMonCol:   128,
		UnivMonTopK:  2,
		UseBigUM:     true,
	}
	h, err := NewHydra(cfg)
	if err != nil {
		t.Fatalf("unexpected error constructing Hydra: %v", err)
	}
	h.SetTopKEnabled(false)

	hash := common.FromString("delta").Hash
	h.UpdateWithHash(hash, 11)
	before := h.EstimateWithHash(hash)

	data, err := h.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize failed: %v", err)
	}
	restored, err := DeserializeHydraFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}
	after := restored.EstimateWithHash(hash)
	if before != after {
		t.Fatalf("roundtrip mismatch: before=%d after=%d", before, after)
	}
}
