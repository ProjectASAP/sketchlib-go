package hydrasketch

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func TestHydraRustStyleSubsetFanout(t *testing.T) {
	h, err := NewHydra(HydraConfig{
		D:             3,
		W:             64,
		CounterType:   HydraCounterCM,
		CounterRows:   3,
		CounterCols:   128,
		FanoutSubkeys: true,
	})
	if err != nil {
		t.Fatalf("new hydra failed: %v", err)
	}

	v := common.FromString("payload")
	h.UpdateValue("city=device;os=linux", v, 5)

	q := FrequencyQuery(v)
	gotCity := h.QueryKey([]string{"city=device"}, q)
	gotOS := h.QueryKey([]string{"os=linux"}, q)
	gotBoth := h.QueryKey([]string{"city=device", "os=linux"}, q)

	if gotCity < 5 || gotOS < 5 || gotBoth < 5 {
		t.Fatalf("fanout mismatch city=%v os=%v both=%v", gotCity, gotOS, gotBoth)
	}
}

func TestMultiHeadHydraRustStyle(t *testing.T) {
	freqCounter, err := NewHydraCountMinCounter(3, 128)
	if err != nil {
		t.Fatalf("new freq counter failed: %v", err)
	}
	latCounter, err := NewHydraCountMinCounter(3, 128)
	if err != nil {
		t.Fatalf("new latency counter failed: %v", err)
	}

	mh, err := NewMultiHeadHydra(3, 64, []HydraDimension{
		{Name: "freq", Counter: freqCounter},
		{Name: "latency", Counter: latCounter},
	})
	if err != nil {
		t.Fatalf("new multihead hydra failed: %v", err)
	}

	freqVal := common.FromString("req-id")
	latVal := common.FromString("p95")

	mh.Update("service=api;region=id", []MultiHeadValue{
		{Value: freqVal, Dimensions: []string{"freq"}},
		{Value: latVal, Dimensions: []string{"latency"}},
	}, 3)

	freqGot := mh.QueryKey([]string{"service=api"}, "freq", FrequencyQuery(freqVal))
	latGot := mh.QueryKey([]string{"region=id"}, "latency", FrequencyQuery(latVal))

	if freqGot < 3 || latGot < 3 {
		t.Fatalf("multihead mismatch freq=%v latency=%v", freqGot, latGot)
	}
}
