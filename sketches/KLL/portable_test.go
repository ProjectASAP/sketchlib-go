package kll

import (
	"encoding/hex"
	"math"
	"testing"

	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// buildKLL feeds values into a seeded sketch (deterministic bytes).
func buildKLL(t *testing.T, k int, seed int64, values []float64) *KLLSketch {
	t.Helper()
	s, err := NewKLLSketchWithSeed(k, seed)
	if err != nil {
		t.Fatalf("NewKLLSketchWithSeed: %v", err)
	}
	for _, v := range values {
		s.Update(v)
	}
	return s
}

func retained(s *KLLSketch) []float64 {
	return append([]float64(nil), s.items...)
}

// TestValueOffsetEncodeRoundTripExact verifies the fixed-point encoder
// round-trips integer and fixed-decimal series exactly, and that the chosen
// scale is the expected one.
func TestValueOffsetEncodeRoundTripExact(t *testing.T) {
	// Integer-ish (scale 0).
	ints := make([]float64, 256)
	for i := range ints {
		ints[i] = float64(1_000_000 + i*7)
	}
	off, sc, res, ok := encodeValueOffset(ints)
	if !ok {
		t.Fatal("integer series should be representable")
	}
	if sc != 0 {
		t.Fatalf("integer series should pick scale 0, got %d", sc)
	}
	back := decodeValueOffset(off, sc, res)
	for i := range ints {
		if back[i] != ints[i] {
			t.Fatalf("int round-trip mismatch at %d: got %v want %v", i, back[i], ints[i])
		}
	}

	// Fixed-decimal (milli resolution → scale -3).
	decis := make([]float64, 128)
	for i := range decis {
		decis[i] = 100.0 + float64(i)*0.001
	}
	o2, s2, r2, ok2 := encodeValueOffset(decis)
	if !ok2 {
		t.Fatal("milli-decimal series should be representable")
	}
	if s2 > 0 || s2 < -3 {
		t.Fatalf("expected a small negative scale, got %d", s2)
	}
	back2 := decodeValueOffset(o2, s2, r2)
	for i := range decis {
		if back2[i] != decis[i] {
			t.Fatalf("decimal round-trip mismatch at %d: got %v want %v", i, back2[i], decis[i])
		}
	}
}

// TestValueOffsetGuardFallsBackToRawF64 verifies the exactness guard rejects
// values not representable at any candidate scale, forcing the raw-f64 path.
func TestValueOffsetGuardFallsBackToRawF64(t *testing.T) {
	cases := [][]float64{
		{0.0, 1.0, math.Pi, 3.0}, // irrational
		{},                       // empty
		{1.0, math.NaN()},        // NaN
		{1.0, math.Inf(1)},       // +Inf
	}
	for i, items := range cases {
		if _, _, _, ok := encodeValueOffset(items); ok {
			t.Fatalf("case %d: guard should have rejected %v", i, items)
		}
	}

	// End-to-end: a sketch with an irrational value must serialize as raw f64
	// (items[] populated, residuals empty).
	s := buildKLL(t, 200, 1, []float64{1.0, 2.0, math.Pi, 4.0})
	env, err := s.SerializePortable()
	if err != nil {
		t.Fatalf("SerializePortable: %v", err)
	}
	st := env.GetKll()
	if len(st.GetResiduals()) != 0 {
		t.Fatal("irrational series should NOT use the value-offset form")
	}
	if len(st.GetItems()) == 0 {
		t.Fatal("raw-f64 fallback must populate items[]")
	}
}

// TestSerializeDefaultUsesValueOffset confirms that for an integer-ish series
// the default SerializePortable emits the value-offset form (residuals set,
// items empty) and that the raw-f64 escape hatch still emits the legacy form.
func TestSerializeDefaultUsesValueOffset(t *testing.T) {
	values := make([]float64, 5000)
	for i := range values {
		values[i] = float64(i + 1)
	}
	s := buildKLL(t, 200, 42, values)

	env, err := s.SerializePortable()
	if err != nil {
		t.Fatalf("SerializePortable: %v", err)
	}
	st := env.GetKll()
	if len(st.GetResiduals()) == 0 {
		t.Fatal("integer series should use the value-offset form")
	}
	if len(st.GetItems()) != 0 {
		t.Fatal("value-offset form must leave items[] empty")
	}

	envRaw, err := s.SerializePortableRawF64()
	if err != nil {
		t.Fatalf("SerializePortableRawF64: %v", err)
	}
	stRaw := envRaw.GetKll()
	if len(stRaw.GetItems()) == 0 {
		t.Fatal("raw-f64 serialization must populate items[]")
	}
	if len(stRaw.GetResiduals()) != 0 {
		t.Fatal("raw-f64 serialization must not emit residuals")
	}
}

// TestProtoRoundTripBothForms verifies the decoder dual-reads both wire forms
// and reconstructs the identical retained set and identical quantiles.
func TestProtoRoundTripBothForms(t *testing.T) {
	values := make([]float64, 5000)
	for i := range values {
		values[i] = float64(i + 1)
	}
	s := buildKLL(t, 200, 42, values)
	want := retained(s)

	// Value-offset form (default).
	fpBytes, err := s.SerializeProtoBytes()
	if err != nil {
		t.Fatalf("SerializeProtoBytes: %v", err)
	}
	fpSketch, err := DeserializeKLLSketchFromProtoBytes(fpBytes)
	if err != nil {
		t.Fatalf("deserialize value-offset: %v", err)
	}

	// Raw-f64 form.
	rawEnv, err := s.SerializePortableRawF64()
	if err != nil {
		t.Fatalf("SerializePortableRawF64: %v", err)
	}
	rawBytes, err := proto.Marshal(rawEnv)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	rawSketch, err := DeserializeKLLSketchFromProtoBytes(rawBytes)
	if err != nil {
		t.Fatalf("deserialize raw-f64: %v", err)
	}

	// Retained set is bit-exact for both paths.
	for i := range want {
		if fpSketch.items[i] != want[i] {
			t.Fatalf("value-offset round-trip item mismatch at %d: %v != %v", i, fpSketch.items[i], want[i])
		}
		if rawSketch.items[i] != want[i] {
			t.Fatalf("raw-f64 round-trip item mismatch at %d: %v != %v", i, rawSketch.items[i], want[i])
		}
	}

	// Quantiles are identical before/after and across both forms
	// (shift-equivariance: q(X−c)=q(X)−c, and c is restored on decode).
	for _, q := range []float64{0.0, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 1.0} {
		orig := s.Quantile(q)
		if got := fpSketch.Quantile(q); got != orig {
			t.Fatalf("value-offset quantile mismatch at p=%v: %v != %v", q, got, orig)
		}
		if got := rawSketch.Quantile(q); got != orig {
			t.Fatalf("raw-f64 quantile mismatch at p=%v: %v != %v", q, got, orig)
		}
	}
}

// TestValueOffsetSmallerThanRawF64 asserts the size win for an integer-ish
// series: the value-offset envelope must be strictly smaller than the raw-f64
// envelope.
func TestValueOffsetSmallerThanRawF64(t *testing.T) {
	values := make([]float64, 5000)
	for i := range values {
		// Latency-ish integers in [1, ~1e5]; integer-exact at scale 0.
		values[i] = float64(1 + (i*37)%100000)
	}
	s := buildKLL(t, 200, 7, values)

	fpEnv, _ := s.SerializePortable()
	rawEnv, _ := s.SerializePortableRawF64()
	fpBytes, _ := proto.Marshal(fpEnv)
	rawBytes, _ := proto.Marshal(rawEnv)

	retainedN := s.GetRetainedItems()
	t.Logf("retained=%d  value-offset=%d bytes  raw-f64=%d bytes  (raw items f64=%d)",
		retainedN, len(fpBytes), len(rawBytes), retainedN*8)
	if len(fpBytes) >= len(rawBytes) {
		t.Fatalf("value-offset (%d) not smaller than raw-f64 (%d)", len(fpBytes), len(rawBytes))
	}
}

// goldenValueOffsetInput is the deterministic cross-language fixture: integers
// 1..=50 with k=200 (no compaction → exact order statistics), value-offset
// encoded (offset=1, scale=0, residuals=0..=49).
func goldenValueOffsetEnvelope(t *testing.T) *envpb.SketchEnvelope {
	t.Helper()
	values := make([]float64, 50)
	for i := range values {
		values[i] = float64(i + 1)
	}
	s := buildKLL(t, 200, 42, values)
	env, err := s.SerializePortable()
	if err != nil {
		t.Fatalf("SerializePortable: %v", err)
	}
	// Clear producer + hash_spec so the golden is stable across versions and
	// matches the Rust consumer's cleared-envelope recipe.
	env.Producer = nil
	env.HashSpec = nil
	return env
}

// TestGoldenValueOffsetEnvelopeForRust pins the exact bytes that the Rust
// consumer's `golden_value_offset_envelope_from_go` test decodes. Run with
// `-v` to print the hex when (re)generating the Rust-side constant.
func TestGoldenValueOffsetEnvelopeForRust(t *testing.T) {
	env := goldenValueOffsetEnvelope(t)
	st := env.GetKll()
	if len(st.GetResiduals()) != 50 || len(st.GetItems()) != 0 {
		t.Fatalf("golden must be value-offset form: residuals=%d items=%d",
			len(st.GetResiduals()), len(st.GetItems()))
	}
	if st.GetOffset() != 1.0 || st.GetValueScale() != 0 {
		t.Fatalf("golden offset=%v scale=%d (want 1.0 / 0)", st.GetOffset(), st.GetValueScale())
	}
	b, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	t.Logf("GO_VALUE_OFFSET_GOLDEN_HEX (%d bytes):\n%s", len(b), hex.EncodeToString(b))

	// Decode back through our own dual-reader for self-consistency.
	got, err := DeserializeKLLSketchFromProtoBytes(b)
	if err != nil {
		t.Fatalf("re-decode golden: %v", err)
	}
	for i := 0; i < 50; i++ {
		if got.items[i] != float64(i+1) {
			t.Fatalf("golden item %d = %v, want %v", i, got.items[i], float64(i+1))
		}
	}
	if got.Quantile(0.0) != 1.0 || got.Quantile(1.0) != 50.0 {
		t.Fatalf("golden quantiles p0=%v p100=%v", got.Quantile(0.0), got.Quantile(1.0))
	}
}

// TestRustGoldenDecodesInGo is the reverse-direction cross-language guard:
// decode the value-offset envelope hex captured from the Rust producer and
// confirm Go reconstructs the identical retained set. The hex is produced and
// asserted by the Rust `golden_value_offset_envelope_for_go` test.
func TestRustGoldenDecodesInGo(t *testing.T) {
	b, err := hex.DecodeString(RustValueOffsetGoldenHex)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	s, err := DeserializeKLLSketchFromProtoBytes(b)
	if err != nil {
		t.Fatalf("deserialize rust golden: %v", err)
	}
	for i := 0; i < 50; i++ {
		if s.items[i] != float64(i+1) {
			t.Fatalf("rust golden item %d = %v, want %v", i, s.items[i], float64(i+1))
		}
	}
	if s.Quantile(0.0) != 1.0 || s.Quantile(1.0) != 50.0 {
		t.Fatalf("rust golden quantiles p0=%v p100=%v", s.Quantile(0.0), s.Quantile(1.0))
	}
}

// RustValueOffsetGoldenHex is the value-offset envelope for (1..=50) emitted by
// asap_sketchlib's encoder (producer/hash_spec cleared). Captured from the Rust
// `golden_value_offset_envelope_for_go` test.
const RustValueOffsetGoldenHex = "08016a4808c801100818012202003239000000000000f03f4a3200020406080a0c0e10121416181a1c1e20222426282a2c2e30323436383a3c3e40424446484a4c4e50525456585a5c5e6062"
