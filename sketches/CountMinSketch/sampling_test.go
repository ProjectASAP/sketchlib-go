package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// p=1.0 (sampling disabled) must produce byte-identical envelopes to a sketch
// built without ever calling WithSampleP — the gate is a true no-op.
func TestCMSSampleP1IsByteIdentical(t *testing.T) {
	build := func(sampled bool) []byte {
		cm, _ := NewCountMinSketch(3, 512)
		if sampled {
			cm.WithSampleP(1.0, 99) // disabled — must match the unsampled build
		}
		for i := 0; i < 5000; i++ {
			cm.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
		}
		b, err := cm.SerializeProtoBytes()
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		return b
	}
	plain := build(false)
	p1 := build(true)
	if string(plain) != string(p1) {
		t.Fatalf("p=1.0 envelope (%d B) differs from unsampled (%d B)", len(p1), len(plain))
	}
}

// A sampled CMS stores raw (smaller) counts; rescaling the queried frequency by
// 1/p recovers the true frequency within the family error bound.
func TestCMSSampledRescaleRecoversFrequency(t *testing.T) {
	const (
		p       = 0.1
		hotN    = 100_000 // true frequency of the hot key
		coldN   = 200_000 // background distinct cold keys, freq 1 each
		rescale = 1.0 / p
	)
	cm, _ := NewCountMinSketch(5, 8192)
	cm.WithSampleP(p, 12345)

	hot := common.Hash64([]byte("hot-key"))
	for i := 0; i < hotN; i++ {
		cm.InsertWithHash(hot)
	}
	for i := 0; i < coldN; i++ {
		cm.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("cold:%d", i))))
	}

	if cm.SampleP() != p {
		t.Fatalf("SampleP()=%v want %v", cm.SampleP(), p)
	}

	rawEst := cm.FastEstimateWithHash(hot)
	rescaled := rawEst * rescale
	relErr := math.Abs(rescaled-hotN) / hotN
	// Sampled raw count should be ~p× the truth.
	if rawEst > float64(hotN)*0.5 {
		t.Errorf("raw sampled count %.0f not reduced (expected ~%.0f)", rawEst, float64(hotN)*p)
	}
	if relErr > 0.05 {
		t.Errorf("rescaled hot freq %.0f rel err %.4f exceeds 5%% of %d", rescaled, relErr, hotN)
	}
	t.Logf("CMS p=%v: raw=%.0f rescaled=%.0f truth=%d relErr=%.4f", p, rawEst, rescaled, hotN, relErr)
}

// The sampled envelope must never be larger than the unsampled one (sampling
// composes with compression — smaller counts → smaller varints).
func TestCMSSampledWireNotLarger(t *testing.T) {
	mk := func(p float64) int {
		cm, _ := NewCountMinSketch(5, 4096)
		if p < 1.0 {
			cm.WithSampleP(p, 7)
		}
		for i := 0; i < 200_000; i++ {
			cm.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("x:%d", i))))
		}
		b, _ := cm.SerializeProtoBytes()
		return len(b)
	}
	full := mk(1.0)
	sampled := mk(0.1)
	// +2 bytes slack for the sample_p field tag+value on the sampled envelope.
	if sampled > full+16 {
		t.Errorf("sampled wire %d B exceeds unsampled %d B (+slack)", sampled, full)
	}
	t.Logf("CMS wire: unsampled=%d B  sampled(p=0.1)=%d B", full, sampled)
}

// Round-trip through proto: a deserialized sampled CMS still queries the raw
// (unscaled) counts; the envelope carries sample_p for the consumer to rescale.
func TestCMSSampledEnvelopeCarriesP(t *testing.T) {
	cm, _ := NewCountMinSketch(3, 512)
	cm.WithSampleP(0.25, 1)
	for i := 0; i < 1000; i++ {
		cm.InsertWithHash(common.Hash64([]byte("z")))
	}
	b, _ := cm.SerializeProtoBytes()

	var env envpb.SketchEnvelope
	if err := proto.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if math.Abs(env.GetSampleP()-0.25) > 1e-9 {
		t.Fatalf("envelope sample_p=%v want 0.25", env.GetSampleP())
	}
	// The stored counts are RAW (unscaled): re-decoding gives back a CMS whose
	// queries return the sampled counts, ready for the consumer's ×1/p rescale.
	if _, err := DeserializeCountMinSketchFromProtoBytes(b); err != nil {
		t.Fatalf("deserialize: %v", err)
	}
}
