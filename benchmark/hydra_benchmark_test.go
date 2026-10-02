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
	hydraBenchOnce sync.Once
	hydraBenchKeys []string
	hydraBenchErr  error
)

func loadCAIDAHydraBenchmark(tb testing.TB) []string {
	tb.Helper()
	hydraBenchOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			hydraBenchErr = err
			return
		}
		keys := make([]string, len(samples))
		var ip [4]byte
		for i, s := range samples {
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			keys[i] = fmt.Sprintf("%08x", ip)
		}
		hydraBenchKeys = keys
	})

	if hydraBenchErr != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", hydraBenchErr)
	}
	if len(hydraBenchKeys) == 0 {
		tb.Skip("Skipping CAIDA benchmark: empty dataset")
	}
	return hydraBenchKeys
}

func mustNewHydraBenchmark(tb testing.TB) *hydrasketch.Hydra {
	tb.Helper()
	counter, err := hydrasketch.NewHydraUnivMonCounter[string](64, 3, 512, 4)
	if err != nil {
		tb.Fatalf("new counter: %v", err)
	}
	h, err := hydrasketch.NewHydra(4, 64, []string{"src"}, counter)
	if err != nil {
		tb.Fatalf("new hydra: %v", err)
	}
	return h
}

func fillHydraBenchmark(tb testing.TB, h *hydrasketch.Hydra, keys []string) {
	tb.Helper()
	for _, k := range keys {
		if err := h.Update([]string{k}, common.FromString(k), 1); err != nil {
			tb.Fatalf("update: %v", err)
		}
	}
}

func BenchmarkHydra_Update_CAIDA(b *testing.B) {
	keys := loadCAIDAHydraBenchmark(b)
	n := len(keys)
	h := mustNewHydraBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%n]
		_ = h.Update([]string{k}, common.FromString(k), 1)
	}
}

func TestHydra_Insert_Latency_P50P99(t *testing.T) {
	keys := loadCAIDAHydraBenchmark(t)
	h := mustNewHydraBenchmark(t)

	sampleSize := benchMinInt(20_000, len(keys))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		k := []string{keys[i]}
		v := common.FromString(keys[i])
		start := time.Now()
		_ = h.Update(k, v, 1)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Hydra Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("===================================")
}

func BenchmarkHydra_QueryCardinality_CAIDA(b *testing.B) {
	keys := loadCAIDAHydraBenchmark(b)
	n := len(keys)
	h := mustNewHydraBenchmark(b)
	fillHydraBenchmark(b, h, keys)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = h.QueryKey([]*string{hydrasketch.Eq(keys[i%n])}, hydrasketch.CardinalityQuery())
	}
}

func TestHydra_Query_Latency_Distribution(t *testing.T) {
	keys := loadCAIDAHydraBenchmark(t)
	h := mustNewHydraBenchmark(t)
	fillHydraBenchmark(t, h, keys)

	sampleSize := 5_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		k := []*string{hydrasketch.Eq(keys[i%len(keys)])}
		start := time.Now()
		_, _ = h.QueryKey(k, hydrasketch.CardinalityQuery())
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Hydra Query Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("========================================")
}

func BenchmarkHydra_MarshalASAPv1_CAIDA(b *testing.B) {
	keys := loadCAIDAHydraBenchmark(b)
	h := mustNewHydraBenchmark(b)
	fillHydraBenchmark(b, h, keys)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.MarshalASAPv1(); err != nil {
			b.Fatalf("marshal failed: %v", err)
		}
	}
}

func BenchmarkHydra_Merge_CAIDA(b *testing.B) {
	keys := loadCAIDAHydraBenchmark(b)
	src := mustNewHydraBenchmark(b)
	fillHydraBenchmark(b, src, keys)
	dst := mustNewHydraBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := dst.Merge(src); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}
