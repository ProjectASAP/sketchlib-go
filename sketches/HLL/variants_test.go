package hll

import (
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func TestHLLVariantRegularAndDataFusion(t *testing.T) {
	reg := NewRegular()
	df := NewDataFusion()
	for i := 0; i < 1000; i++ {
		in := common.FromU64(uint64(i))
		reg.Update(in)
		df.Update(in)
	}
	if reg.Estimate() <= 0 || df.Estimate() <= 0 {
		t.Fatalf("invalid estimates regular=%d df=%d", reg.Estimate(), df.Estimate())
	}
}

func TestHLLHIPBasic(t *testing.T) {
	hip := NewHIP()
	for i := 0; i < 1000; i++ {
		hip.Update(common.FromU64(uint64(i)))
	}
	if hip.Estimate() <= 0 {
		t.Fatalf("invalid hip estimate: %d", hip.Estimate())
	}
}

// The expected values are asap_sketchlib's HyperLogLog<Classic>::estimate for
// the same registers.
func TestHLLRegularEstimateHighCardinality(t *testing.T) {
	cases := []struct {
		name string
		reg  func(i int) uint8
		want int
	}{
		{"all 17", func(int) uint8 { return 17 }, 1548877950},
		{"all 18", func(int) uint8 { return 18 }, 3097755901},
		{"all 30", func(int) uint8 { return 30 }, 12688408174182},
		{"alternating 20 and 40", func(i int) uint8 { return uint8(20 + 20*(i%2)) }, 24782023581},
		{"i mod 46", func(i int) uint8 { return uint8(i % 46) }, 271165},
		{"384 ones", func(i int) uint8 { return uint8(min(1, max(0, 384-i))) }, 388},
	}
	for _, c := range cases {
		h := NewRegular()
		regs := h.Registers.AsMutSlice()
		for i := range regs {
			regs[i] = c.reg(i)
		}
		if got := h.Estimate(); got != c.want {
			t.Errorf("%s: estimate %d, want %d", c.name, got, c.want)
		}
	}
}
