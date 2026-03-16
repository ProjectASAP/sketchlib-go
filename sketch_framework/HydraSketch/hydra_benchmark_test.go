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
	hydraCAIDAOnce   sync.Once
	hydraCAIDAKeys   []string
	hydraCAIDAHashes []uint64
	hydraCAIDAErr    error
)

func loadHydraCAIDA(b *testing.B) ([]string, []uint64) {
	b.Helper()
	hydraCAIDAOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			hydraCAIDAErr = err
			return
		}
		keys := make([]string, len(samples))
		hashes := make([]uint64, len(samples))
		var ip [4]byte
		for i, s := range samples {
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			keys[i] = fmt.Sprintf("%08x", ip)
			hashes[i] = common.Hash64(ip[:])
		}
		hydraCAIDAKeys = keys
		hydraCAIDAHashes = hashes
	})
	if hydraCAIDAErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", hydraCAIDAErr)
	}
	if len(hydraCAIDAKeys) == 0 || len(hydraCAIDAHashes) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return hydraCAIDAKeys, hydraCAIDAHashes
}

func mustNewHydra(tb testing.TB, enableTopK bool) *Hydra {
	tb.Helper()
	h, err := NewHydra(HydraConfig{
		D:                   4,
		W:                   64,
		CounterType:         HydraCounterUniversal,
		UniversalLayer:      4,
		UniversalRow:        3,
		UniversalCol:        512,
		UniversalTopK:       64,
		EnableGlobalCounter: true,
	})
	if err != nil {
		tb.Fatalf("new hydra: %v", err)
	}
	if !enableTopK {
		h.SetTopKEnabled(false)
	}
	return h
}

func BenchmarkHydra_Update_CAIDA(b *testing.B) {
	keys, _ := loadHydraCAIDA(b)
	n := len(keys)
	h := mustNewHydra(b, true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%n]
		h.UpdateValue(k, common.FromString(k), 1)
	}
}

func BenchmarkHydra_UpdateWithHash_CAIDA(b *testing.B) {
	_, hashes := loadHydraCAIDA(b)
	n := len(hashes)
	h := mustNewHydra(b, false)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.UpdateWithHash(hashes[i%n], 1)
	}
}

func BenchmarkHydra_QueryFrequency_CAIDA(b *testing.B) {
	keys, _ := loadHydraCAIDA(b)
	n := len(keys)

	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateValue(k, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%n]
		_ = h.QueryFrequency([]string{k}, common.FromString(k))
	}
}

func BenchmarkHydra_TopK_CAIDA(b *testing.B) {
	keys, _ := loadHydraCAIDA(b)
	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateValue(k, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.TopK(100)
	}
}

func BenchmarkHydra_Serialize_CAIDA(b *testing.B) {
	keys, _ := loadHydraCAIDA(b)
	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateValue(k, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.SerializeToBytes(); err != nil {
			b.Fatalf("serialize failed: %v", err)
		}
	}
}
