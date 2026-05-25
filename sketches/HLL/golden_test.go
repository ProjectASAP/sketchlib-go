package hll

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"google.golang.org/protobuf/proto"
)

// goldenCardinalities are the sample cardinalities the cross-language golden
// fixtures cover. All are below SparseCrossoverNonZero so the Go encoder emits
// the SPARSE representation, exercising the wire path the Rust decoder must
// read.
var goldenCardinalities = []int{0, 50, 1000, 5000}

// goldenHLL builds a deterministic HLL for a given cardinality. The Rust golden
// test does NOT rebuild the sketch; it decodes the committed bytes and checks
// against the committed register list, so only Go needs the builder.
func goldenHLL(card int) *HyperLogLog {
	h := NewHyperLogLog()
	for i := 0; i < card; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("golden:%d", i))))
	}
	return h
}

// fixtureBytes returns the proto-marshaled SketchEnvelope (sparse) and the
// expected non-zero register list ("index:value\n" lines, sorted by index).
func fixtureBytes(t *testing.T, h *HyperLogLog) (pb []byte, regsTxt string) {
	t.Helper()
	env, err := h.SerializePortable()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	pb, err = proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var lines []string
	for i, v := range h.RegisterSlice() {
		if v != 0 {
			lines = append(lines, fmt.Sprintf("%d:%d", i, v))
		}
	}
	sort.Slice(lines, func(a, b int) bool {
		// numeric sort by index
		ia := atoiIndex(lines[a])
		ib := atoiIndex(lines[b])
		return ia < ib
	})
	return pb, strings.Join(lines, "\n") + "\n"
}

func atoiIndex(line string) int {
	n := 0
	for _, c := range line {
		if c == ':' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// TestGoldenSelfVerify reconstructs each golden sketch from its own
// freshly-serialized SPARSE bytes and asserts the register array is exact. This
// is the Go-side guard that the encoder/decoder are mutually consistent.
func TestGoldenSelfVerify(t *testing.T) {
	for _, card := range goldenCardinalities {
		h := goldenHLL(card)
		want := append([]uint8(nil), h.RegisterSlice()...)
		pb, _ := fixtureBytes(t, h)

		got, err := DeserializeHyperLogLogFromProtoBytes(pb)
		if err != nil {
			t.Fatalf("card=%d deserialize: %v", card, err)
		}
		assertRegistersEqual(t, want, got.RegisterSlice())
	}
}

// TestGenerateGoldenFixtures writes the cross-language golden fixtures when
// XGEN_DIR is set. Run with:
//
//	XGEN_DIR=/path/to/asap_sketchlib/src/message_pack_format/portable/testdata \
//	  go test ./sketches/HLL/ -run TestGenerateGoldenFixtures
//
// It is a no-op (skipped) during normal `go test ./...`.
func TestGenerateGoldenFixtures(t *testing.T) {
	dir := os.Getenv("XGEN_DIR")
	if dir == "" {
		t.Skip("XGEN_DIR not set; skipping golden fixture generation")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, card := range goldenCardinalities {
		h := goldenHLL(card)
		pb, regsTxt := fixtureBytes(t, h)

		hexName := filepath.Join(dir, fmt.Sprintf("hll_sparse_%d.pb.hex", card))
		regsName := filepath.Join(dir, fmt.Sprintf("hll_sparse_%d.regs.txt", card))
		if err := os.WriteFile(hexName, []byte(hex.EncodeToString(pb)+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", hexName, err)
		}
		if err := os.WriteFile(regsName, []byte(regsTxt), 0o644); err != nil {
			t.Fatalf("write %s: %v", regsName, err)
		}
		t.Logf("wrote card=%d: %d sparse bytes, %d nonzero regs",
			card, len(pb), countNonZero(h.RegisterSlice()))
	}
}
