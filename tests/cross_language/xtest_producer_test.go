// xtest_producer — Cross-language integration test: Go producer side.
//
// Inserts synthetic data into nine sketch types, serializes each as a portable
// protobuf SketchEnvelope, and writes the binary files to $XTEST_DIR.
//
// Output files:
//
//	countmin.pb     CountMinState     (float64 counters)
//	kll.pb          KLLState          (quantile items + coin RNG)
//	ddsketch.pb     DDSketchState     (alpha + bucket array)
//	hll.pb          HyperLogLogState  (DataFusion estimator)
//	countsketch.pb  CountSketchState  (float64 signed counters)
//	coco.pb         CocoSketchState   (hash+val+hasKey buckets)
//	elastic.pb      ElasticState      (heavy buckets + light CM)
//	univmon.pb      UnivMonState      (layered CS + TopK heaps)
//	hydra.pb        HydraState        (CM-cell grid)
//
// Usage:
//
//	XTEST_DIR=<path> go test -v -run TestXtestProducer ./tests/cross_language/
package cross_language_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ProjectASAP/sketchlib-go/common"
	cocosketch "github.com/ProjectASAP/sketchlib-go/sketches/CocoSketch"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	elasticsketch "github.com/ProjectASAP/sketchlib-go/sketches/ElasticSKetch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
	hydrasketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/HydraSketch"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
)

func TestXtestProducer(t *testing.T) {
	outDir := os.Getenv("XTEST_DIR")
	if outDir == "" {
		t.Fatal("XTEST_DIR env var not set")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	t.Log("=======================================================")
	t.Log("  sketchlib-go → xtest_producer")
	t.Log("=======================================================")

	// -----------------------------------------------------------------------
	// CountMin
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[CountMin] Step 1/3 — Create sketch (3 rows × 512 cols)")
	cm, err := countminsketch.NewCountMinSketch(3, 512)
	tcheck(t, "new countmin", err)

	t.Log("[CountMin] Step 2/3 — Insert 10 000 items + 100 extra for 'item:42'")
	for i := 0; i < 10_000; i++ {
		cm.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("item:%d", i))))
	}
	hotHash := common.Hash64([]byte("item:42"))
	for i := 0; i < 100; i++ {
		cm.InsertWithHash(hotHash)
	}
	t.Logf("[CountMin] Step 3/3 — 'item:42' freq = %.0f (expect ≥ 101)",
		cm.FastEstimateWithHash(hotHash))
	writeEnvelope(t, outDir, "countmin.pb", tmust(cm.SerializePortable()))

	// -----------------------------------------------------------------------
	// KLL
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[KLL] Step 1/3 — Create sketch (k=200)")
	sk := kll.New()

	t.Log("[KLL] Step 2/3 — Insert values 1.0 … 10 000.0")
	for i := 1; i <= 10_000; i++ {
		sk.Update(float64(i))
	}
	t.Logf("[KLL] Step 3/3 — p50≈%.1f  p99≈%.1f", sk.Quantile(0.50), sk.Quantile(0.99))
	writeEnvelope(t, outDir, "kll.pb", tmust(sk.SerializePortable()))

	// -----------------------------------------------------------------------
	// DDSketch
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[DDSketch] Step 1/3 — Create sketch (alpha=0.01)")
	ds := ddsketch.New(0.01)

	t.Log("[DDSketch] Step 2/3 — Insert values 1.0 … 10 000.0")
	for i := 1; i <= 10_000; i++ {
		ds.Update(float64(i))
	}
	p50dd, _ := ds.Quantile(0.50)
	p99dd, _ := ds.Quantile(0.99)
	t.Logf("[DDSketch] Step 3/3 — p50≈%.2f  p99≈%.2f", p50dd, p99dd)
	writeEnvelope(t, outDir, "ddsketch.pb", tmust(ds.SerializePortable()))

	// -----------------------------------------------------------------------
	// HLL (DataFusion estimator)
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[HLL] Step 1/3 — Create HyperLogLog sketch")
	h := hll.NewHyperLogLog()

	t.Log("[HLL] Step 2/3 — Insert 50 000 distinct keys")
	for i := 0; i < 50_000; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("hll:%d", i))))
	}
	t.Logf("[HLL] Step 3/3 — cardinality≈%d (expect ~50000)", h.Estimate())
	writeEnvelope(t, outDir, "hll.pb", tmust(h.SerializePortable()))

	// -----------------------------------------------------------------------
	// CountSketch
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[CountSketch] Step 1/3 — Create sketch (3 rows × 512 cols)")
	cs, err := countsketch.NewCountSketch(3, 512)
	tcheck(t, "new countsketch", err)

	t.Log("[CountSketch] Step 2/3 — Insert 10 000 items + 200 extra for 'cs:hot'")
	for i := 0; i < 10_000; i++ {
		cs.InsertWithHashAndValue(common.Hash64([]byte(fmt.Sprintf("cs:%d", i))), 1)
	}
	hotCSKey := []byte("cs:hot")
	hotCSHash := common.Hash64(hotCSKey)
	for i := 0; i < 200; i++ {
		cs.InsertWithHashAndValue(hotCSHash, 1)
	}
	csEst, _ := cs.QueryWithHash(common.QueryFrequency, hotCSHash)
	t.Logf("[CountSketch] Step 3/3 — 'cs:hot' est = %.0f (expect ≥ 200)", csEst)
	writeEnvelope(t, outDir, "countsketch.pb", tmust(cs.SerializePortable()))

	// -----------------------------------------------------------------------
	// CocoSketch
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[CocoSketch] Step 1/3 — Create sketch (d=5, width=128)")
	coco, err := cocosketch.NewCocoSketch(5, 128)
	tcheck(t, "new coco", err)

	t.Log("[CocoSketch] Step 2/3 — Insert 'coco:hot' with val 500")
	coco.Insert("coco:hot", 500)
	for i := 0; i < 1000; i++ {
		coco.Insert(fmt.Sprintf("coco:%d", i), 1)
	}
	cocoEst := coco.Estimate("coco:hot")
	t.Logf("[CocoSketch] Step 3/3 — 'coco:hot' est = %d (expect ≥ 500)", cocoEst)
	writeEnvelope(t, outDir, "coco.pb", tmust(coco.SerializePortable()))

	// -----------------------------------------------------------------------
	// ElasticSketch
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[ElasticSketch] Step 1/3 — Create sketch (BucketCount=64)")
	es, err := elasticsketch.New(elasticsketch.Config{BucketCount: 64})
	tcheck(t, "new elastic", err)

	t.Log("[ElasticSketch] Step 2/3 — Insert 'elephant' 1000×, others 1×")
	for i := 0; i < 1000; i++ {
		es.Insert("elephant")
	}
	for i := 0; i < 5000; i++ {
		es.Insert(fmt.Sprintf("flow:%d", i))
	}
	elasticEst := es.Query("elephant")
	t.Logf("[ElasticSketch] Step 3/3 — 'elephant' est = %d (expect ≥ 900)", elasticEst)
	writeEnvelope(t, outDir, "elastic.pb", tmust(es.SerializePortable()))

	// -----------------------------------------------------------------------
	// UnivMon
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[UnivMon] Step 1/3 — Create sketch (k=32, row=5, col=512, layer=8)")
	um, err := univmon.NewUnivSketchPyramid(32, 5, 512, 8)
	tcheck(t, "new univmon", err)

	t.Log("[UnivMon] Step 2/3 — Insert 10 000 distinct keys")
	for i := 0; i < 10_000; i++ {
		um.Update(common.FromString(fmt.Sprintf("um:%d", i)), 1)
	}
	card := um.GetCardinality()
	t.Logf("[UnivMon] Step 3/3 — cardinality≈%.0f (expect ~10000)", card)
	writeEnvelope(t, outDir, "univmon.pb", tmust(um.SerializePortable()))

	// -----------------------------------------------------------------------
	// HydraSketch (CountMin cells, D=4, W=4)
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("[Hydra] Step 1/3 — Create sketch (D=4, W=4, CM cells)")
	hydra, err := hydrasketch.NewHydra(hydrasketch.HydraConfig{
		D:           4,
		W:           4,
		CounterType: hydrasketch.HydraCounterCM,
		CounterRows: 3,
		CounterCols: 512,
		EnableTopK:  false,
	})
	tcheck(t, "new hydra", err)

	t.Log("[Hydra] Step 2/3 — Insert 10 000 items (key=value as subkey)")
	for i := 0; i < 10_000; i++ {
		key := fmt.Sprintf("hydra:%d", i)
		hydra.UpdateWithInput(common.FromString(key), 1)
	}
	hotHydraKey := "hydra:42"
	for i := 0; i < 50; i++ {
		hydra.UpdateWithInput(common.FromString(hotHydraKey), 1)
	}
	hotHydraEst := hydra.QueryFrequency([]string{hotHydraKey}, common.FromString(hotHydraKey))
	t.Logf("[Hydra] Step 3/3 — 'hydra:42' est = %.0f (expect ≥ 51)", hotHydraEst)
	writeEnvelope(t, outDir, "hydra.pb", tmust(hydra.SerializePortable()))

	// -----------------------------------------------------------------------
	// Summary
	// -----------------------------------------------------------------------
	t.Log()
	t.Log("=======================================================")
	t.Log("  Producer complete — 9 sketches written to " + outDir)
	t.Log("=======================================================")
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func writeEnvelope(t *testing.T, dir, name string, env proto.Message) {
	t.Helper()
	data, err := proto.Marshal(env)
	tcheck(t, "marshal "+name, err)
	path := filepath.Join(dir, name)
	tcheck(t, "write "+path, os.WriteFile(path, data, 0o644))
	t.Logf("   → %s  (%d bytes)", path, len(data))
}

// tmust does not take *testing.T so Go can unpack multi-return calls:
//
//	writeEnvelope(t, dir, "x.pb", tmust(sketch.SerializePortable()))
func tmust(env proto.Message, err error) proto.Message {
	if err != nil {
		panic(fmt.Sprintf("[serialize]: %v", err))
	}
	return env
}

func tcheck(t *testing.T, ctx string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("[%s]: %v", ctx, err)
	}
}
