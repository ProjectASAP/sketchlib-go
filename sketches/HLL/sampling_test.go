package hll

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// p=1.0 (sampling disabled) must produce byte-identical envelopes to a sketch
// built without ever calling WithSampleP.
func TestHLLSampleP1IsByteIdentical(t *testing.T) {
	build := func(sampled bool) []byte {
		h := NewHyperLogLog()
		if sampled {
			h.WithSampleP(1.0)
		}
		for i := 0; i < 20_000; i++ {
			h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
		}
		b, err := h.SerializeProtoBytes()
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

// Sampling thins the registers, so the sampled wire form is never larger.
func TestHLLSampledWireNotLarger(t *testing.T) {
	mk := func(p float64) int {
		h := NewHyperLogLog()
		if p < 1.0 {
			h.WithSampleP(p)
		}
		for i := 0; i < 5000; i++ {
			h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("x:%d", i))))
		}
		b, _ := h.SerializeProtoBytes()
		return len(b)
	}
	full := mk(1.0)
	sampled := mk(0.1)
	if sampled > full+16 {
		t.Errorf("sampled wire %d B exceeds unsampled %d B (+slack)", sampled, full)
	}
	t.Logf("HLL wire: unsampled=%d B  sampled(p=0.1)=%d B", full, sampled)
}

func TestHLLSampledEnvelopeCarriesP(t *testing.T) {
	h := NewHyperLogLog()
	h.WithSampleP(0.2)
	for i := 0; i < 1000; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("z:%d", i))))
	}
	b, _ := h.SerializeProtoBytes()
	var env envpb.SketchEnvelope
	if err := proto.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if math.Abs(env.GetSampleP()-0.2) > 1e-9 {
		t.Fatalf("envelope sample_p=%v want 0.2", env.GetSampleP())
	}
}
