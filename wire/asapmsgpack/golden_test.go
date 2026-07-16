package asapmsgpack

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cross-language ASAPv1 golden byte-vector tests. The golden .hex files in
// ../../asapv1_golden/ are authored by the Rust reference encoder and checked in
// byte-identically to asap_sketchlib/asapv1_golden/. Each test proves, for a
// fixed known sketch state:
//
//	(a) Unmarshal(golden) then re-Marshal == golden  (Go decode/encode round-trip)
//	(b) Marshal(equivalent known state) == golden     (cross-language parity proof)
//
// (b) is the actual proof that Go emits the same bytes Rust does. See
// asapv1_golden/README.md.

func loadGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "asapv1_golden", name+".hex"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decode golden %s: %v", name, err)
	}
	return b
}

// p12Registers is the known P12 register pattern shared by all three HLL
// fixtures (mostly zero with a few set bytes at known indices).
func p12Registers() []byte {
	r := make([]byte, 4096)
	r[0] = 1
	r[1] = 7
	r[100] = 42
	r[4095] = 3
	return r
}

func hllGoldenCase(t *testing.T, name string, variant HLLVariant, hip0, hip1, hipEst float64) {
	want := loadGolden(t, name)
	regs := p12Registers()

	state := HLLSketchState{
		Variant:   variant,
		Precision: 12,
		Registers: regs,
		HipKxq0:   hip0,
		HipKxq1:   hip1,
		HipEst:    hipEst,
	}

	// (b) Marshal known state == golden (cross-language parity proof).
	got, err := MarshalHLLSketch(state)
	if err != nil {
		t.Fatalf("MarshalHLLSketch(%s): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: Go Marshal diverges from Rust golden\n got=%x\nwant=%x", name, got, want)
	}

	// (a) Unmarshal(golden) then re-Marshal == golden.
	decoded, err := UnmarshalHLLSketch(want)
	if err != nil {
		t.Fatalf("UnmarshalHLLSketch(%s): %v", name, err)
	}
	if decoded.Variant != variant || decoded.Precision != 12 {
		t.Fatalf("%s: decoded variant/precision = %q/%d", name, decoded.Variant, decoded.Precision)
	}
	if !bytes.Equal(decoded.Registers, regs) {
		t.Fatalf("%s: decoded registers differ from known state", name)
	}
	if variant == HLLVariantHip {
		if decoded.HipKxq0 != hip0 || decoded.HipKxq1 != hip1 || decoded.HipEst != hipEst {
			t.Fatalf("%s: decoded HIP scalars = %v/%v/%v, want %v/%v/%v",
				name, decoded.HipKxq0, decoded.HipKxq1, decoded.HipEst, hip0, hip1, hipEst)
		}
	}
	reMarshaled, err := MarshalHLLSketch(decoded)
	if err != nil {
		t.Fatalf("re-MarshalHLLSketch(%s): %v", name, err)
	}
	if !bytes.Equal(reMarshaled, want) {
		t.Fatalf("%s: re-Marshal after Unmarshal diverges from golden", name)
	}
}

func TestGoldenHLLClassicP12(t *testing.T) {
	hllGoldenCase(t, "hll_classic_p12", HLLVariantRegular, 0, 0, 0)
}

func TestGoldenHLLErtlMLEP12(t *testing.T) {
	hllGoldenCase(t, "hll_ertl_mle_p12", HLLVariantDatafusion, 0, 0, 0)
}

func TestGoldenHLLHIPP12(t *testing.T) {
	hllGoldenCase(t, "hll_hip_p12", HLLVariantHip, 1.5, 2.5, 3.0)
}

func TestGoldenCMSI64Regular2x3(t *testing.T) {
	want := loadGolden(t, "cms_i64_regular_2x3")

	state := CountMinWireState{
		Rows:        2,
		Cols:        3,
		CounterType: CMSCounterI64,
		Mode:        CMSModeRegular,
		CountsI64:   []int64{0, 1, 127, 128, 300, 65536}, // row-major [[0,1,127],[128,300,65536]]
	}

	// (b) Marshal known state == golden.
	got, err := MarshalCountMinSketchState(state)
	if err != nil {
		t.Fatalf("MarshalCountMinSketchState: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cms_i64_regular_2x3: Go Marshal diverges from Rust golden\n got=%x\nwant=%x", got, want)
	}

	// (a) Unmarshal(golden) then re-Marshal == golden.
	decoded, err := UnmarshalCountMinSketchState(want)
	if err != nil {
		t.Fatalf("UnmarshalCountMinSketchState: %v", err)
	}
	if decoded.CounterType != CMSCounterI64 || decoded.Mode != CMSModeRegular {
		t.Fatalf("decoded counter_type/mode = %q/%q", decoded.CounterType, decoded.Mode)
	}
	if decoded.Rows != 2 || decoded.Cols != 3 {
		t.Fatalf("decoded rows/cols = %d/%d", decoded.Rows, decoded.Cols)
	}
	wantCounts := []int64{0, 1, 127, 128, 300, 65536}
	if len(decoded.CountsI64) != len(wantCounts) {
		t.Fatalf("decoded %d counts, want %d", len(decoded.CountsI64), len(wantCounts))
	}
	for i, v := range wantCounts {
		if decoded.CountsI64[i] != v {
			t.Fatalf("decoded count[%d]=%d, want %d", i, decoded.CountsI64[i], v)
		}
	}
	reMarshaled, err := MarshalCountMinSketchState(decoded)
	if err != nil {
		t.Fatalf("re-MarshalCountMinSketchState: %v", err)
	}
	if !bytes.Equal(reMarshaled, want) {
		t.Fatalf("cms_i64_regular_2x3: re-Marshal after Unmarshal diverges from golden")
	}
}

func TestGoldenCMSF64Fast2x3(t *testing.T) {
	want := loadGolden(t, "cms_f64_fast_2x3")

	flat := []float64{0.0, 1.5, 2.25, 3.75, 4.125, 5.0625} // row-major
	state := CountMinWireState{
		Rows:        2,
		Cols:        3,
		CounterType: CMSCounterF64,
		Mode:        CMSModeFast,
		CountsF64:   flat,
	}

	// (b) Marshal known state == golden.
	got, err := MarshalCountMinSketchState(state)
	if err != nil {
		t.Fatalf("MarshalCountMinSketchState: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cms_f64_fast_2x3: Go Marshal diverges from Rust golden\n got=%x\nwant=%x", got, want)
	}

	// The higher-level [][]float64 API (f64/fast) must produce the same bytes.
	matrix := [][]float64{{0.0, 1.5, 2.25}, {3.75, 4.125, 5.0625}}
	gotMatrix, err := MarshalCountMinSketch(2, 3, matrix)
	if err != nil {
		t.Fatalf("MarshalCountMinSketch: %v", err)
	}
	if !bytes.Equal(gotMatrix, want) {
		t.Fatalf("cms_f64_fast_2x3: MarshalCountMinSketch diverges from golden")
	}

	// (a) Unmarshal(golden) then re-Marshal == golden.
	decoded, err := UnmarshalCountMinSketchState(want)
	if err != nil {
		t.Fatalf("UnmarshalCountMinSketchState: %v", err)
	}
	if decoded.CounterType != CMSCounterF64 || decoded.Mode != CMSModeFast {
		t.Fatalf("decoded counter_type/mode = %q/%q", decoded.CounterType, decoded.Mode)
	}
	if len(decoded.CountsF64) != len(flat) {
		t.Fatalf("decoded %d counts, want %d", len(decoded.CountsF64), len(flat))
	}
	for i, v := range flat {
		if decoded.CountsF64[i] != v {
			t.Fatalf("decoded count[%d]=%v, want %v", i, decoded.CountsF64[i], v)
		}
	}
	reMarshaled, err := MarshalCountMinSketchState(decoded)
	if err != nil {
		t.Fatalf("re-MarshalCountMinSketchState: %v", err)
	}
	if !bytes.Equal(reMarshaled, want) {
		t.Fatalf("cms_f64_fast_2x3: re-Marshal after Unmarshal diverges from golden")
	}
}
