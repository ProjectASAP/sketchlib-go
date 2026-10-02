package countminsketch

import (
	"fmt"
	"math"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// p=1.0 (sampling disabled) must produce byte-identical encodings to a sketch
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
		b, err := cm.MarshalASAPv1()
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		return b
	}
	plain := build(false)
	p1 := build(true)
	if string(plain) != string(p1) {
		t.Fatalf("p=1.0 encoding (%d B) differs from unsampled (%d B)", len(p1), len(plain))
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

// A sampled sketch's raw counts cannot be encoded without its probability.
func TestCMSSampledRejectsASAPv1(t *testing.T) {
	cm, _ := NewCountMinSketch(3, 512)
	cm.WithSampleP(0.25, 1)
	for i := 0; i < 1000; i++ {
		cm.InsertWithHash(common.Hash64([]byte("z")))
	}
	if _, err := cm.MarshalASAPv1(); err == nil {
		t.Fatal("MarshalASAPv1 encoded a sampled sketch")
	}
}
