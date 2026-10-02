package hydrasketch

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	hydraCAIDAOnce sync.Once
	hydraCAIDAKeys []string
	hydraCAIDAErr  error
)

func loadHydraCAIDA(b *testing.B) []string {
	b.Helper()
	hydraCAIDAOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			hydraCAIDAErr = err
			return
		}
		keys := make([]string, len(samples))
		var ip [4]byte
		for i, s := range samples {
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			keys[i] = fmt.Sprintf("%08x", ip)
		}
		hydraCAIDAKeys = keys
	})
	if hydraCAIDAErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", hydraCAIDAErr)
	}
	if len(hydraCAIDAKeys) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return hydraCAIDAKeys
}

func newBenchHydra(b *testing.B) *Hydra {
	b.Helper()
	counter, err := NewHydraUnivMonCounter[string](64, 3, 512, 4)
	if err != nil {
		b.Fatal(err)
	}
	h, err := NewHydra(4, 64, []string{"src"}, counter)
	if err != nil {
		b.Fatal(err)
	}
	return h
}

func BenchmarkHydra_Update_CAIDA(b *testing.B) {
	keys := loadHydraCAIDA(b)
	n := len(keys)
	h := newBenchHydra(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%n]
		_ = h.Update([]string{k}, common.FromString(k), 1)
	}
}

func BenchmarkHydra_QueryCardinality_CAIDA(b *testing.B) {
	keys := loadHydraCAIDA(b)
	n := len(keys)
	h := newBenchHydra(b)
	for _, k := range keys {
		_ = h.Update([]string{k}, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = h.QueryKey([]*string{Eq(keys[i%n])}, CardinalityQuery())
	}
}

func BenchmarkHydra_MarshalASAPv1_CAIDA(b *testing.B) {
	keys := loadHydraCAIDA(b)
	h := newBenchHydra(b)
	for _, k := range keys {
		_ = h.Update([]string{k}, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.MarshalASAPv1(); err != nil {
			b.Fatalf("marshal failed: %v", err)
		}
	}
}
