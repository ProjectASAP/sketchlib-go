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
		D:            4,
		W:            64,
		UnivMonLayer: 4,
		UnivMonRow:   3,
		UnivMonCol:   512,
		UnivMonTopK:  64,
		UseBigUM:     true,
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
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _ := loadHydraCAIDA(b)
	n := len(keys)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing Hydra (TopK enabled)")
	h := mustNewHydra(b, true)

	b.Log("[STEP 3] Running key update benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.UpdateN(keys[i%n], 1)
	}
}

func BenchmarkHydra_UpdateWithHash_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	_, hashes := loadHydraCAIDA(b)
	n := len(hashes)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing Hydra (TopK disabled)")
	h := mustNewHydra(b, false)

	b.Log("[STEP 3] Running hash fast-path update benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.UpdateWithHash(hashes[i%n], 1)
	}
}

func BenchmarkHydra_Estimate_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _ := loadHydraCAIDA(b)
	n := len(keys)

	b.Log("[STEP 2] Building and pre-filling Hydra")
	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateN(k, 1)
	}

	b.Log("[STEP 3] Running estimate benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Estimate(keys[i%n])
	}
}

func BenchmarkHydra_EstimateWithHash_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	_, hashes := loadHydraCAIDA(b)
	n := len(hashes)

	b.Log("[STEP 2] Building and pre-filling Hydra")
	h := mustNewHydra(b, false)
	for _, hash := range hashes {
		h.UpdateWithHash(hash, 1)
	}

	b.Log("[STEP 3] Running hash estimate benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.EstimateWithHash(hashes[i%n])
	}
}

func BenchmarkHydra_TopK_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _ := loadHydraCAIDA(b)

	b.Log("[STEP 2] Building and pre-filling Hydra")
	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateN(k, 1)
	}

	b.Log("[STEP 3] Running top-k benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.TopK(100)
	}
}

func BenchmarkHydra_Serialize_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _ := loadHydraCAIDA(b)

	b.Log("[STEP 2] Building and pre-filling Hydra")
	h := mustNewHydra(b, true)
	for _, k := range keys {
		h.UpdateN(k, 1)
	}

	b.Log("[STEP 3] Running serialize benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.SerializeToBytes(); err != nil {
			b.Fatalf("serialize failed: %v", err)
		}
	}
}
