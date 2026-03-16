package benchmark

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	elasticsketch "github.com/ProjectASAP/sketchlib-go/sketches/ElasticSKetch"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	elasticBenchOnce   sync.Once
	elasticBenchKeys   []string
	elasticBenchHashes []uint64
	elasticBenchErr    error
)

func loadCAIDAElasticBenchmark(tb testing.TB) ([]string, []uint64) {
	tb.Helper()
	elasticBenchOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			elasticBenchErr = err
			return
		}
		keys := make([]string, len(samples))
		hashes := make([]uint64, len(samples))
		for i, s := range samples {
			var ip [4]byte
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			keys[i] = fmt.Sprintf("%08x", ip)
			hashes[i] = common.Hash64(ip[:])
		}
		elasticBenchKeys = keys
		elasticBenchHashes = hashes
	})

	if elasticBenchErr != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", elasticBenchErr)
	}
	if len(elasticBenchKeys) == 0 || len(elasticBenchHashes) == 0 {
		tb.Skip("Skipping CAIDA benchmark: empty dataset")
	}
	return elasticBenchKeys, elasticBenchHashes
}

func mustNewElasticBenchmark(tb testing.TB) *elasticsketch.ElasticSketch {
	tb.Helper()
	es, err := elasticsketch.New(elasticsketch.Config{BucketCount: 4096})
	if err != nil {
		tb.Fatalf("new elastic: %v", err)
	}
	return es
}

func preloadElasticBenchmark(es *elasticsketch.ElasticSketch, keys []string) {
	for _, k := range keys {
		es.Insert(k)
	}
}

func cloneElasticBenchmark(tb testing.TB, src *elasticsketch.ElasticSketch) *elasticsketch.ElasticSketch {
	tb.Helper()
	data, err := src.SerializeToBytes()
	if err != nil {
		tb.Fatalf("serialize elastic: %v", err)
	}
	dst, err := elasticsketch.DeserializeElasticSketchFromBytes(data)
	if err != nil {
		tb.Fatalf("deserialize elastic: %v", err)
	}
	return dst
}

// =====================================================
// 1. INSERT PERFORMANCE & LATENCY
// =====================================================

func BenchmarkElastic_Insert_Single(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	n := len(keys)
	es := mustNewElasticBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.Insert(keys[i%n])
	}
}

func BenchmarkElastic_Insert_Batch(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	n := len(keys)
	es := mustNewElasticBenchmark(b)
	batchSize := 1000

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := (i * batchSize) % n
		end := start + batchSize
		if end > n {
			end = n
		}
		for j := start; j < end; j++ {
			es.Insert(keys[j])
		}
	}
}

func BenchmarkElastic_InsertWithHash_Single(b *testing.B) {
	_, hashes := loadCAIDAElasticBenchmark(b)
	n := len(hashes)
	es := mustNewElasticBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.InsertWithHash(hashes[i%n])
	}
}

func TestElastic_Insert_Latency_P50P99(t *testing.T) {
	_, hashes := loadCAIDAElasticBenchmark(t)
	es := mustNewElasticBenchmark(t)

	sampleSize := benchMinInt(20_000, len(hashes))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		es.InsertWithHash(hashes[i])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Elastic Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=====================================")
}

// =====================================================
// 2. QUERY PERFORMANCE & LATENCY
// =====================================================

func BenchmarkElastic_Query_Throughput(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	n := len(keys)
	es := mustNewElasticBenchmark(b)
	preloadElasticBenchmark(es, keys)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = es.Query(keys[i%n])
	}
}

func BenchmarkElastic_Query_HotKey(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	es := mustNewElasticBenchmark(b)
	preloadElasticBenchmark(es, keys)
	hotKey := keys[0]

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = es.Query(hotKey)
	}
}

func TestElastic_Query_Latency_P99(t *testing.T) {
	keys, _ := loadCAIDAElasticBenchmark(t)
	es := mustNewElasticBenchmark(t)
	preloadElasticBenchmark(es, keys)

	sampleSize := 5_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_ = es.Query(keys[i%len(keys)])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Elastic Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:        %d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("====================================")
}

// =====================================================
// 3. MERGE PERFORMANCE & LATENCY
// =====================================================

func BenchmarkElastic_Merge_2(b *testing.B) {
	left := mustNewElasticBenchmark(b)
	right := mustNewElasticBenchmark(b)
	left.InsertN("flow:left", 20)
	right.InsertN("flow:right", 15)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := cloneElasticBenchmark(b, left)
		_ = target.Merge(right)
	}
}

func BenchmarkElastic_Merge_10(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	list := make([]*elasticsketch.ElasticSketch, 10)
	for i := range list {
		list[i] = mustNewElasticBenchmark(b)
	}
	for i, key := range keys[:benchMinInt(len(keys), 10000)] {
		list[i%len(list)].Insert(key)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := cloneElasticBenchmark(b, list[0])
		for j := 1; j < len(list); j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkElastic_Merge_100(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	list := make([]*elasticsketch.ElasticSketch, 100)
	for i := range list {
		list[i] = mustNewElasticBenchmark(b)
	}
	for i, key := range keys[:benchMinInt(len(keys), 100000)] {
		list[i%len(list)].Insert(key)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := cloneElasticBenchmark(b, list[0])
		for j := 1; j < len(list); j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkElastic_Merge_1000(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	list := make([]*elasticsketch.ElasticSketch, 1000)
	for i := range list {
		list[i] = mustNewElasticBenchmark(b)
	}
	for i, key := range keys[:benchMinInt(len(keys), 200000)] {
		list[i%len(list)].Insert(key)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := cloneElasticBenchmark(b, list[0])
		for j := 1; j < len(list); j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkElastic_Merge_PreFilled_CAIDA(b *testing.B) {
	keys, _ := loadCAIDAElasticBenchmark(b)
	mid := len(keys) / 2

	leftSrc := mustNewElasticBenchmark(b)
	rightSrc := mustNewElasticBenchmark(b)
	preloadElasticBenchmark(leftSrc, keys[:mid])
	preloadElasticBenchmark(rightSrc, keys[mid:])

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		left := cloneElasticBenchmark(b, leftSrc)
		right := cloneElasticBenchmark(b, rightSrc)
		if err := left.Merge(right); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}

func TestElastic_Merge_Latency_Distribution(t *testing.T) {
	keys, _ := loadCAIDAElasticBenchmark(t)
	mid := len(keys) / 2

	leftSrc := mustNewElasticBenchmark(t)
	rightSrc := mustNewElasticBenchmark(t)
	preloadElasticBenchmark(leftSrc, keys[:mid])
	preloadElasticBenchmark(rightSrc, keys[mid:])

	sampleSize := 500
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left := cloneElasticBenchmark(t, leftSrc)
		right := cloneElasticBenchmark(t, rightSrc)
		start := time.Now()
		if err := left.Merge(right); err != nil {
			t.Fatalf("merge failed: %v", err)
		}
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== Elastic Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==========================================")
}
