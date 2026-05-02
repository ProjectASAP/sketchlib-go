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
