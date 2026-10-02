package benchmark

import (
	"encoding/binary"
	"sort"
	"sync"
	"testing"
	"time"

	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	univBenchOnce sync.Once
	univBenchKeys []string
	univBenchErr  error
)

func loadCAIDAUnivMonBenchmark(tb testing.TB) []string {
	tb.Helper()
	univBenchOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			univBenchErr = err
			return
		}
		keys := make([]string, len(samples))
		for i, s := range samples {
			var ip [4]byte
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			keys[i] = string(ip[:])
		}
		univBenchKeys = keys
	})

	if univBenchErr != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", univBenchErr)
	}
	if len(univBenchKeys) == 0 {
		tb.Skip("Skipping CAIDA benchmark: empty dataset")
	}
	return univBenchKeys
}

func mustNewUnivMonBenchmark(tb testing.TB) *univmon.UnivMon[string] {
	tb.Helper()
	us, err := univmon.NewUnivMon[string](200, 5, 4096, 16)
	if err != nil {
		tb.Fatalf("new univmon: %v", err)
	}
	return us
}

func fillUnivMonBenchmark(tb testing.TB, us *univmon.UnivMon[string], keys []string) {
	tb.Helper()
	for _, key := range keys {
		if err := us.Insert(key, 1); err != nil {
			tb.Fatalf("insert: %v", err)
		}
	}
}

func cloneUnivMonBenchmark(tb testing.TB, src *univmon.UnivMon[string]) *univmon.UnivMon[string] {
	tb.Helper()
	data, err := src.MarshalASAPv1()
	if err != nil {
		tb.Fatalf("marshal univmon: %v", err)
	}
	dst := new(univmon.UnivMon[string])
	if err := dst.UnmarshalASAPv1(data); err != nil {
		tb.Fatalf("unmarshal univmon: %v", err)
	}
	return dst
}

func BenchmarkUnivMon_Insert_CAIDA(b *testing.B) {
	keys := loadCAIDAUnivMonBenchmark(b)
	n := len(keys)
	us := mustNewUnivMonBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = us.Insert(keys[i%n], 1)
	}
}

func TestUnivMon_Insert_Latency_P50P99(t *testing.T) {
	keys := loadCAIDAUnivMonBenchmark(t)
	us := mustNewUnivMonBenchmark(t)

	sampleSize := benchMinInt(20_000, len(keys))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_ = us.Insert(keys[i], 1)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=====================================")
}

func BenchmarkUnivMon_QueryCardinality_CAIDA(b *testing.B) {
	keys := loadCAIDAUnivMonBenchmark(b)
	us := mustNewUnivMonBenchmark(b)
	fillUnivMonBenchmark(b, us, keys)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = us.CalcCard()
	}
}

func TestUnivMon_Query_Latency_Distribution(t *testing.T) {
	keys := loadCAIDAUnivMonBenchmark(t)
	us := mustNewUnivMonBenchmark(t)
	fillUnivMonBenchmark(t, us, keys)

	sampleSize := 5_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_ = us.CalcCard()
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Query Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==========================================")
}

func BenchmarkUnivMon_Merge_CAIDA(b *testing.B) {
	keys := loadCAIDAUnivMonBenchmark(b)
	mid := len(keys) / 2

	leftSrc := mustNewUnivMonBenchmark(b)
	rightSrc := mustNewUnivMonBenchmark(b)
	fillUnivMonBenchmark(b, leftSrc, keys[:mid])
	fillUnivMonBenchmark(b, rightSrc, keys[mid:])

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		left := cloneUnivMonBenchmark(b, leftSrc)
		right := cloneUnivMonBenchmark(b, rightSrc)
		b.StartTimer()
		if err := left.Merge(right); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}

func BenchmarkUnivMon_Marshal_CAIDA(b *testing.B) {
	keys := loadCAIDAUnivMonBenchmark(b)
	us := mustNewUnivMonBenchmark(b)
	fillUnivMonBenchmark(b, us, keys)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := us.MarshalASAPv1(); err != nil {
			b.Fatalf("marshal failed: %v", err)
		}
	}
}

func TestUnivMon_Merge_Latency_Distribution(t *testing.T) {
	keys := loadCAIDAUnivMonBenchmark(t)
	mid := len(keys) / 2

	leftSrc := mustNewUnivMonBenchmark(t)
	rightSrc := mustNewUnivMonBenchmark(t)
	fillUnivMonBenchmark(t, leftSrc, keys[:mid])
	fillUnivMonBenchmark(t, rightSrc, keys[mid:])

	sampleSize := 250
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left := cloneUnivMonBenchmark(t, leftSrc)
		right := cloneUnivMonBenchmark(t, rightSrc)
		start := time.Now()
		if err := left.Merge(right); err != nil {
			t.Fatalf("merge failed: %v", err)
		}
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==========================================")
}
