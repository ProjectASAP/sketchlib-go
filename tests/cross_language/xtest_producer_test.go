// xtest_producer — Cross-language integration test: Go producer side.
//
// Inserts synthetic data into two sketch types, serializes each as a portable
// protobuf SketchEnvelope, and writes the binary files to $XTEST_DIR.
//
// Output files:
//
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
	t.Log("  Producer complete — 2 sketches written to " + outDir)
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
