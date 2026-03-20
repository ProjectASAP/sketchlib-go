// xtest_producer — Cross-language integration test: Go producer side.
//
// Inserts synthetic data into nine sketch types, serializes each as a portable
// protobuf SketchEnvelope, and writes the binary files to <outdir>.
//
// Output files:
//   countmin.pb     CountMinState     (float64 counters)
//   kll.pb          KLLState          (quantile items + coin RNG)
//   ddsketch.pb     DDSketchState     (alpha + bucket array)
//   hll.pb          HyperLogLogState  (DataFusion estimator)
//   countsketch.pb  CountSketchState  (float64 signed counters)
//   coco.pb         CocoSketchState   (hash+val+hasKey buckets)
//   elastic.pb      ElasticState      (heavy buckets + light CM)
//   univmon.pb      UnivMonState      (layered CS + TopK heaps)
//   hydra.pb        HydraState        (CM-cell grid)
//
// Usage:
//   go run ./cmd/xtest_producer <outdir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: xtest_producer <outdir>")
		os.Exit(1)
	}
	outDir := os.Args[1]
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}

	fmt.Println("=======================================================")
	fmt.Println("  sketchlib-go → xtest_producer")
	fmt.Println("=======================================================")

	// -----------------------------------------------------------------------
	// CountMin
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[CountMin] Step 1/3 — Create sketch (3 rows × 512 cols)")
	cm, err := countminsketch.NewCountMinSketch(3, 512)
	check("new countmin", err)

	fmt.Println("[CountMin] Step 2/3 — Insert 10 000 items + 100 extra for 'item:42'")
	for i := 0; i < 10_000; i++ {
		cm.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("item:%d", i))))
	}
	hotHash := common.Hash64([]byte("item:42"))
	for i := 0; i < 100; i++ {
		cm.InsertWithHash(hotHash)
	}
	fmt.Printf("[CountMin] Step 3/3 — 'item:42' freq = %.0f (expect ≥ 101)\n",
		cm.FastEstimateWithHash(hotHash))
	writeEnvelope(outDir, "countmin.pb", must(cm.SerializePortable()))

	// -----------------------------------------------------------------------
	// KLL
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[KLL] Step 1/3 — Create sketch (k=200)")
	sk := kll.New()

	fmt.Println("[KLL] Step 2/3 — Insert values 1.0 … 10 000.0")
	for i := 1; i <= 10_000; i++ {
		sk.Insert(float64(i))
	}
	fmt.Printf("[KLL] Step 3/3 — p50≈%.1f  p99≈%.1f\n", sk.Quantile(0.50), sk.Quantile(0.99))
	writeEnvelope(outDir, "kll.pb", must(sk.SerializePortable()))

	// -----------------------------------------------------------------------
	// DDSketch
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[DDSketch] Step 1/3 — Create sketch (alpha=0.01)")
	ds := ddsketch.New(0.01)

	fmt.Println("[DDSketch] Step 2/3 — Insert values 1.0 … 10 000.0")
	for i := 1; i <= 10_000; i++ {
		ds.Add(float64(i))
	}
	p50dd, _ := ds.GetValueAtQuantile(0.50)
	p99dd, _ := ds.GetValueAtQuantile(0.99)
	fmt.Printf("[DDSketch] Step 3/3 — p50≈%.2f  p99≈%.2f\n", p50dd, p99dd)
	writeEnvelope(outDir, "ddsketch.pb", must(ds.SerializePortable()))

	// -----------------------------------------------------------------------
	// HLL (DataFusion estimator)
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[HLL] Step 1/3 — Create HyperLogLog sketch")
	h := hll.NewHyperLogLog()

	fmt.Println("[HLL] Step 2/3 — Insert 50 000 distinct keys")
	for i := 0; i < 50_000; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("hll:%d", i))))
	}
	fmt.Printf("[HLL] Step 3/3 — cardinality≈%d (expect ~50000)\n", h.EstimateCardinality())
	writeEnvelope(outDir, "hll.pb", must(h.SerializePortable()))

	// -----------------------------------------------------------------------
	// CountSketch
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[CountSketch] Step 1/3 — Create sketch (3 rows × 512 cols)")
	cs, err := countsketch.NewCountSketch(3, 512)
	check("new countsketch", err)

	fmt.Println("[CountSketch] Step 2/3 — Insert 10 000 items + 200 extra for 'cs:hot'")
	for i := 0; i < 10_000; i++ {
		cs.InsertWithHashAndValue(common.Hash64([]byte(fmt.Sprintf("cs:%d", i))), 1)
	}
	hotCSKey := []byte("cs:hot")
	hotCSHash := common.Hash64(hotCSKey)
	for i := 0; i < 200; i++ {
		cs.InsertWithHashAndValue(hotCSHash, 1)
	}
	csEst, _ := cs.QueryWithHash(common.QueryFrequency, hotCSHash)
	fmt.Printf("[CountSketch] Step 3/3 — 'cs:hot' est = %.0f (expect ≥ 200)\n", csEst)
	writeEnvelope(outDir, "countsketch.pb", must(cs.SerializePortable()))

	// -----------------------------------------------------------------------
	// CocoSketch
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[CocoSketch] Step 1/3 — Create sketch (d=5, width=128)")
	coco, err := cocosketch.NewCocoSketch(5, 128)
	check("new coco", err)

	fmt.Println("[CocoSketch] Step 2/3 — Insert 'coco:hot' with val 500")
	coco.Insert("coco:hot", 500)
	for i := 0; i < 1000; i++ {
		coco.Insert(fmt.Sprintf("coco:%d", i), 1)
	}
	cocoEst := coco.Estimate("coco:hot")
	fmt.Printf("[CocoSketch] Step 3/3 — 'coco:hot' est = %d (expect ≥ 500)\n", cocoEst)
	writeEnvelope(outDir, "coco.pb", must(coco.SerializePortable()))

	// -----------------------------------------------------------------------
	// ElasticSketch
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[ElasticSketch] Step 1/3 — Create sketch (BucketCount=64)")
	es, err := elasticsketch.New(elasticsketch.Config{BucketCount: 64})
	check("new elastic", err)

	fmt.Println("[ElasticSketch] Step 2/3 — Insert 'elephant' 1000×, others 1×")
	for i := 0; i < 1000; i++ {
		es.Insert("elephant")
	}
	for i := 0; i < 5000; i++ {
		es.Insert(fmt.Sprintf("flow:%d", i))
	}
	elasticEst := es.Query("elephant")
	fmt.Printf("[ElasticSketch] Step 3/3 — 'elephant' est = %d (expect ≥ 900)\n", elasticEst)
	writeEnvelope(outDir, "elastic.pb", must(es.SerializePortable()))

	// -----------------------------------------------------------------------
	// UnivMon
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[UnivMon] Step 1/3 — Create sketch (k=32, row=5, col=512, layer=8)")
	um, err := univmon.NewUnivSketchPyramid(32, 5, 512, 8)
	check("new univmon", err)

	fmt.Println("[UnivMon] Step 2/3 — Insert 10 000 distinct keys")
	for i := 0; i < 10_000; i++ {
		um.Update(common.FromString(fmt.Sprintf("um:%d", i)), 1)
	}
	card := um.GetCardinality()
	fmt.Printf("[UnivMon] Step 3/3 — cardinality≈%.0f (expect ~10000)\n", card)
	writeEnvelope(outDir, "univmon.pb", must(um.SerializePortable()))

	// -----------------------------------------------------------------------
	// HydraSketch (CountMin cells, D=4, W=4)
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("[Hydra] Step 1/3 — Create sketch (D=4, W=4, CM cells)")
	hydra, err := hydrasketch.NewHydra(hydrasketch.HydraConfig{
		D:           4,
		W:           4,
		CounterType: hydrasketch.HydraCounterCM,
		CounterRows: 3,
		CounterCols: 512,
		EnableTopK:  false,
	})
	check("new hydra", err)

	fmt.Println("[Hydra] Step 2/3 — Insert 10 000 items (key=value as subkey)")
	for i := 0; i < 10_000; i++ {
		key := fmt.Sprintf("hydra:%d", i)
		hydra.UpdateWithInput(common.FromString(key), 1)
	}
	hotHydraKey := "hydra:42"
	for i := 0; i < 50; i++ {
		hydra.UpdateWithInput(common.FromString(hotHydraKey), 1)
	}
	hotHydraEst := hydra.QueryFrequency([]string{hotHydraKey}, common.FromString(hotHydraKey))
	fmt.Printf("[Hydra] Step 3/3 — 'hydra:42' est = %.0f (expect ≥ 51)\n", hotHydraEst)
	writeEnvelope(outDir, "hydra.pb", must(hydra.SerializePortable()))

	// -----------------------------------------------------------------------
	// Summary
	// -----------------------------------------------------------------------
	fmt.Println()
	fmt.Println("=======================================================")
	fmt.Println("  Producer complete — 9 sketches written to " + outDir)
	fmt.Println("=======================================================")
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

type envelope interface {
	ProtoMessage()
}

func writeEnvelope(dir, name string, env proto.Message) {
	data, err := proto.Marshal(env)
	check("marshal "+name, err)
	path := filepath.Join(dir, name)
	check("write "+path, os.WriteFile(path, data, 0o644))
	fmt.Printf("   → %s  (%d bytes)\n", path, len(data))
}

func must(env proto.Message, err error) proto.Message {
	check("serialize", err)
	return env
}

func check(ctx string, err error) {
	if err != nil {
		fatalf("[%s]: %v", ctx, err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL "+format+"\n", args...)
	os.Exit(1)
}
