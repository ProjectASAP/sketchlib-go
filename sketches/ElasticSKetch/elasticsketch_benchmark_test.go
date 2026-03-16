package elasticsketch

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	elasticCAIDAOnce   sync.Once
	elasticCAIDAKeys   []string
	elasticCAIDAInputs []*common.SketchInput
	elasticCAIDAHashes []uint64
	elasticCAIDAErr    error
)

func loadElasticCAIDA(b *testing.B) ([]string, []*common.SketchInput, []uint64) {
	b.Helper()
	elasticCAIDAOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			elasticCAIDAErr = err
			return
		}

		keys := make([]string, len(samples))
		inputs := make([]*common.SketchInput, len(samples))
		hashes := make([]uint64, len(samples))

		for i, s := range samples {
			var ip [4]byte
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))

			keys[i] = fmt.Sprintf("%08x", ip)
			input := common.FromBytes(ip[:])
			inputs[i] = input
			hashes[i] = input.Hash
		}

		elasticCAIDAKeys = keys
		elasticCAIDAInputs = inputs
		elasticCAIDAHashes = hashes
	})

	if elasticCAIDAErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", elasticCAIDAErr)
	}
	if len(elasticCAIDAKeys) == 0 || len(elasticCAIDAInputs) == 0 || len(elasticCAIDAHashes) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}

	return elasticCAIDAKeys, elasticCAIDAInputs, elasticCAIDAHashes
}

func mustNewElastic(tb testing.TB) *ElasticSketch {
	tb.Helper()
	es, err := New(Config{
		BucketCount: 4096,
	})
	if err != nil {
		tb.Fatalf("new elasticsketch: %v", err)
	}
	return es
}

func cloneElastic(tb testing.TB, src *ElasticSketch) *ElasticSketch {
	tb.Helper()
	data, err := src.SerializeToBytes()
	if err != nil {
		tb.Fatalf("serialize elastic: %v", err)
	}
	dst, err := DeserializeElasticSketchFromBytes(data)
	if err != nil {
		tb.Fatalf("deserialize elastic: %v", err)
	}
	return dst
}

func BenchmarkElasticSketch_Insert_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _, _ := loadElasticCAIDA(b)
	n := len(keys)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing ElasticSketch")
	es := mustNewElastic(b)

	b.Log("[STEP 3] Running insert benchmark (string key path)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.Insert(keys[i%n])
	}
}

func BenchmarkElasticSketch_InsertInput_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	_, inputs, _ := loadElasticCAIDA(b)
	n := len(inputs)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing ElasticSketch")
	es := mustNewElastic(b)

	b.Log("[STEP 3] Running insert benchmark (SketchInput path)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.InsertInput(inputs[i%n])
	}
}

func BenchmarkElasticSketch_InsertWithHash_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	_, _, hashes := loadElasticCAIDA(b)
	n := len(hashes)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing ElasticSketch")
	es := mustNewElastic(b)

	b.Log("[STEP 3] Running insert benchmark (pre-hashed fast path)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.InsertWithHash(hashes[i%n])
	}
}

func BenchmarkElasticSketch_Serialize_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _, _ := loadElasticCAIDA(b)

	b.Log("[STEP 2] Building and pre-filling ElasticSketch")
	es := mustNewElastic(b)
	for _, k := range keys {
		es.Insert(k)
	}

	b.Log("[STEP 3] Running serialize benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := es.SerializeToBytes(); err != nil {
			b.Fatalf("serialize failed: %v", err)
		}
	}
}

func BenchmarkElasticSketch_Query_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _, _ := loadElasticCAIDA(b)
	n := len(keys)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Building and pre-filling ElasticSketch")
	es := mustNewElastic(b)
	for _, k := range keys {
		es.Insert(k)
	}

	b.Log("[STEP 3] Running query benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = es.Query(keys[i%n])
	}
}

func BenchmarkElasticSketch_Merge_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA keys")
	keys, _, _ := loadElasticCAIDA(b)
	n := len(keys)
	mid := n / 2
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Building split ElasticSketches")
	leftSrc := mustNewElastic(b)
	rightSrc := mustNewElastic(b)
	for i, key := range keys {
		if i < mid {
			leftSrc.Insert(key)
		} else {
			rightSrc.Insert(key)
		}
	}

	b.Log("[STEP 3] Running merge benchmark (light-part sum merge)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		left := cloneElastic(b, leftSrc)
		right := cloneElastic(b, rightSrc)
		if err := left.Merge(right); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}
