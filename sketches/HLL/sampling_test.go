package hll

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// p=1.0 (sampling disabled) encodes to the same bytes as a sketch built
// without ever calling WithSampleP.
func TestHLLSampleP1IsByteIdentical(t *testing.T) {
	build := func(sampled bool) []byte {
		h := NewHyperLogLog()
		if sampled {
			h.WithSampleP(1.0)
		}
		for i := 0; i < 20_000; i++ {
			h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
		}
		b, err := h.MarshalASAPv1()
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		return b
	}
	if string(build(false)) != string(build(true)) {
		t.Fatal("p=1.0 envelope differs from unsampled")
	}
}

// Hash-threshold sampling is UNBIASED for distinct counting: estimate/p
// recovers the true cardinality within the combined RSE bound, and crucially
// per-occurrence multiplicity must NOT bias it (the same key inserted many
// times is kept-or-dropped consistently).
func TestHLLSampledRescaleUnbiased(t *testing.T) {
	const (
		n = 200_000
		p = 0.1
	)
	h := NewHyperLogLog()
	h.WithSampleP(p)
	for i := 0; i < n; i++ {
		// Insert each distinct key 3× to exercise the "frequency must not bias"
		// property of hash-threshold (vs per-occurrence) sampling.
		hash := common.Hash64([]byte(fmt.Sprintf("hll:%d", i)))
		h.InsertWithHash(hash)
		h.InsertWithHash(hash)
		h.InsertWithHash(hash)
	}
	raw := float64(h.Estimate())
	rescaled := raw / p
	relErr := math.Abs(rescaled-n) / n
	// Combined RSE ≈ sqrt((1-p)/(p n) + 1.04^2/m). With p=0.1, n=2e5, m=16384
	// this is ~0.01; allow generous headroom for a single trial.
	if relErr > 0.05 {
		t.Errorf("rescaled cardinality %.0f rel err %.4f exceeds 5%% of %d", rescaled, relErr, n)
	}
	t.Logf("HLL p=%v: raw≈%.0f rescaled≈%.0f truth=%d relErr=%.4f", p, raw, rescaled, n, relErr)
}
