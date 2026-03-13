package benchmark

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	hydrasketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/HydraSketch"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	hydraBenchOnce   sync.Once
	hydraBenchKeys   []string
	hydraBenchHashes []uint64
	hydraBenchErr    error
)

func loadCAIDAHydraBenchmark(tb testing.TB) ([]string, []uint64) {
	tb.Helper()
	hydraBenchOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			hydraBenchErr = err
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
		hydraBenchKeys = keys
		hydraBenchHashes = hashes
	})

	if hydraBenchErr != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", hydraBenchErr)
	}
	if len(hydraBenchKeys) == 0 || len(hydraBenchHashes) == 0 {
		tb.Skip("Skipping CAIDA benchmark: empty dataset")
	}
	return hydraBenchKeys, hydraBenchHashes
}

func mustNewHydraBenchmark(tb testing.TB, enableTopK bool) *hydrasketch.Hydra {
	tb.Helper()
	h, err := hydrasketch.NewHydra(hydrasketch.HydraConfig{
		D:                   4,
		W:                   64,
		CounterType:         hydrasketch.HydraCounterUniversal,
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
	keys, _ := loadCAIDAHydraBenchmark(b)
	n := len(keys)
	h := mustNewHydraBenchmark(b, true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%n]
		h.UpdateValue(k, common.FromString(k), 1)
	}
}

func BenchmarkHydra_UpdateWithHash_CAIDA(b *testing.B) {
	_, hashes := loadCAIDAHydraBenchmark(b)
	n := len(hashes)
	h := mustNewHydraBenchmark(b, false)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.UpdateWithHash(hashes[i%n], 1)
	}
}

func TestHydra_Insert_Latency_P50P99(t *testing.T) {
	_, hashes := loadCAIDAHydraBenchmark(t)
	h := mustNewHydraBenchmark(t, false)

	sampleSize := benchMinInt(20_000, len(hashes))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		h.UpdateWithHash(hashes[i], 1)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Hydra Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("===================================")
}

func BenchmarkHydra_QueryFrequency_CAIDA(b *testing.B) {
	keys, _ := loadCAIDAHydraBenchmark(b)
	n := len(keys)
	h := mustNewHydraBenchmark(b, true)
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
	keys, _ := loadCAIDAHydraBenchmark(b)
	h := mustNewHydraBenchmark(b, true)
	for _, k := range keys {
		h.UpdateValue(k, common.FromString(k), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.TopK(100)
	}
}

func TestHydra_Query_Latency_Distribution(t *testing.T) {
	keys, _ := loadCAIDAHydraBenchmark(t)
	h := mustNewHydraBenchmark(t, true)
	for _, k := range keys {
		h.UpdateValue(k, common.FromString(k), 1)
	}

	sampleSize := 5_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		k := keys[i%len(keys)]
		start := time.Now()
		_ = h.QueryFrequency([]string{k}, common.FromString(k))
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Hydra Query Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("========================================")
}

func BenchmarkHydra_Serialize_CAIDA(b *testing.B) {
	keys, _ := loadCAIDAHydraBenchmark(b)
	h := mustNewHydraBenchmark(b, true)
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

func BenchmarkHydra_Merge_Unsupported(b *testing.B) {
	b.Skip("Hydra does not expose a merge operation")
}

func TestHydra_Merge_Latency_Distribution(t *testing.T) {
	t.Skip("Hydra does not expose a merge operation")
}
