package hll

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	hllpb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"google.golang.org/protobuf/proto"
)

// deterministicSparseHLL builds an HLL with a fixed, hash-independent set of
// non-zero registers so the encoded bytes are stable and mirror exactly what a
// Rust producer would emit for the same register array. Used for the
// cross-language sparse (tag 7) golden.
func deterministicSparseHLL() *HyperLogLog {
	h := NewHyperLogLog()
	regs := h.Registers.AsMutSlice()
	// A handful of registers at known indices with known values. Index deltas
	// span both <128 (1-byte uvarint) and >=128 (2-byte uvarint) to exercise
	// the multi-byte index-delta path the Rust decoder must read.
	set := map[int]uint8{0: 3, 5: 12, 200: 7, 16383: 51}
	for i, v := range set {
		regs[i] = v
	}
	return h
}

// TestSparseRegistersTag7_CrossLanguageGolden is the P1-1 guard: it pins the
// exact wire bytes of the SPARSE registers (proto tag 7,
// HLLSparseRegisters.packed) for a deterministic register array and verifies a
// full round-trip. The sparse path is now the DEFAULT for most real HLLs
// (<~6000 non-zero registers) yet previously had NO cross-language fixture —
// the xtest producer only inserted enough keys to land DENSE.
//
// The packed layout (index-delta uvarint, value uvarint; first delta is the
// absolute index) is the cross-repo contract decoded by asap_sketchlib's
// `decode_sparse_registers` / `registers_from_state`. Pinning the bytes here
// fails loudly if the Go encoder drifts from that layout.
func TestSparseRegistersTag7_CrossLanguageGolden(t *testing.T) {
	h := deterministicSparseHLL()
	want := append([]uint8(nil), h.RegisterSlice()...)

	env, err := h.SerializePortable()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	st := env.GetHll()
	sp := st.GetRegistersSparse()
	if sp == nil {
		t.Fatal("expected SPARSE (tag 7) encoding")
	}
	if len(st.GetRegisters()) != 0 {
		t.Fatal("dense field must be empty when sparse is used")
	}
	if sp.GetNumRegisters() != uint32(HLLRegisterCount) {
		t.Fatalf("num_registers=%d want %d", sp.GetNumRegisters(), HLLRegisterCount)
	}

	// Golden packed blob: (delta,value) uvarint pairs, ascending by index.
	//   idx 0     : delta 0      -> 00, value 3  -> 03
	//   idx 5     : delta 5      -> 05, value 12 -> 0c
	//   idx 200   : delta 195    -> c301 (uvarint), value 7  -> 07
	//   idx 16383 : delta 16183  -> b77e (uvarint), value 51 -> 33
	const wantPackedHex = "0003050cc30107b77e33"
	if got := hex.EncodeToString(sp.GetPacked()); got != wantPackedHex {
		t.Fatalf("sparse packed bytes drifted from cross-repo contract:\n got=%s\nwant=%s",
			got, wantPackedHex)
	}

	// Round-trip through the dual-reader (mirrors the Rust registers_from_state).
	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	gotSketch, err := DeserializeHyperLogLogFromProtoBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	assertRegistersEqual(t, want, gotSketch.RegisterSlice())
}

// TestHLLDeltaPackedUpdates_CrossLanguageGolden is the P1-1 companion: it pins
// the HLLDelta.packed_updates blob (the sparse register-increase delta) for a
// deterministic set of increased registers and verifies a full round-trip.
// packed_updates shares the (index-delta, value) uvarint layout with the
// sparse full state, so the same Rust unpacker reads it.
func TestHLLDeltaPackedUpdates_CrossLanguageGolden(t *testing.T) {
	// Snapshot: empty. Current: the deterministic sparse register array, so
	// every non-zero register is an "increase" and appears in the delta.
	snap := NewHyperLogLog()
	current := &HyperLogLog{Registers: storage.Vector1DFromSlice(deterministicSparseHLL().RegisterSlice())}

	d := ComputeRegisterDelta(snap, current)
	if len(d.Updates) != 4 {
		t.Fatalf("expected 4 register updates, got %d", len(d.Updates))
	}

	b, err := SerializeRegisterDelta(d)
	if err != nil {
		t.Fatalf("serialize delta: %v", err)
	}
	var msg hllpb.HLLDelta
	if err := proto.Unmarshal(b, &msg); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	// Same (delta,value) layout as the sparse full state above.
	const wantPackedHex = "0003050cc30107b77e33"
	if got := hex.EncodeToString(msg.GetPackedUpdates()); got != wantPackedHex {
		t.Fatalf("packed_updates bytes drifted from cross-repo contract:\n got=%s\nwant=%s",
			got, wantPackedHex)
	}

	// Round-trip: apply the decoded delta onto empty and compare to current.
	decoded, err := DeserializeRegisterDelta(b)
	if err != nil {
		t.Fatalf("deserialize delta: %v", err)
	}
	target := NewHyperLogLog()
	ApplyRegisterDelta(target, decoded)
	for i, v := range current.RegisterSlice() {
		if target.RegisterSlice()[i] != v {
			t.Fatalf("register[%d] after delta apply: got %d want %d", i, target.RegisterSlice()[i], v)
		}
	}
}

// buildHLL inserts `card` distinct keys and returns the populated sketch.
func buildHLL(card int) *HyperLogLog {
	h := NewHyperLogLog()
	for i := 0; i < card; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
	}
	return h
}

// TestSparseRoundTripExact verifies that a sparse-encoded sketch reconstructs
// the exact dense register array across the proto round trip, at cardinalities
// well below the crossover.
func TestSparseRoundTripExact(t *testing.T) {
	for _, card := range []int{0, 1, 100, 1000, 5000} {
		card := card
		t.Run(fmt.Sprintf("card=%d", card), func(t *testing.T) {
			h := buildHLL(card)
			want := append([]uint8(nil), h.RegisterSlice()...)

			env, err := h.SerializePortable()
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			st := env.GetHll()
			if nz := countNonZero(want); nz < SparseCrossoverNonZero {
				if st.GetRegistersSparse() == nil {
					t.Fatalf("expected SPARSE encoding at nz=%d (< %d)", nz, SparseCrossoverNonZero)
				}
				if len(st.GetRegisters()) != 0 {
					t.Fatalf("dense field must be empty when sparse is used")
				}
			}

			data, err := proto.Marshal(env)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := DeserializeHyperLogLogFromProtoBytes(data)
			if err != nil {
				t.Fatalf("deserialize: %v", err)
			}
			assertRegistersEqual(t, want, got.RegisterSlice())
		})
	}
}

// TestDenseRoundTripExact verifies the DENSE path still round-trips exactly,
// using a synthetic register array with non-zero count at/above the crossover.
func TestDenseRoundTripExact(t *testing.T) {
	h := NewHyperLogLog()
	regs := h.Registers.AsMutSlice()
	// Force >= crossover non-zero registers so the encoder picks DENSE.
	for i := 0; i < SparseCrossoverNonZero+500 && i < len(regs); i++ {
		regs[i] = uint8((i % 50) + 1)
	}
	want := append([]uint8(nil), h.RegisterSlice()...)

	env, err := h.SerializePortable()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	st := env.GetHll()
	if st.GetRegistersSparse() != nil {
		t.Fatalf("expected DENSE encoding at nz=%d (>= %d)", countNonZero(want), SparseCrossoverNonZero)
	}
	if len(st.GetRegisters()) != HLLRegisterCount {
		t.Fatalf("dense registers length %d, want %d", len(st.GetRegisters()), HLLRegisterCount)
	}

	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := DeserializeHyperLogLogFromProtoBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	assertRegistersEqual(t, want, got.RegisterSlice())
}

// TestSparseSerializedSize asserts sparse is much smaller than dense at low
// cardinality, and that the dense path stays near the raw-register size at high
// cardinality (sparse never inflates the on-wire payload).
func TestSparseSerializedSize(t *testing.T) {
	// Low cardinality: sparse must be far smaller than the dense full-state size.
	for _, card := range []int{100, 1000} {
		h := buildHLL(card)
		nz := countNonZero(h.RegisterSlice())

		sparseEnv, _ := h.SerializePortable()
		sparseBytes, _ := proto.Marshal(sparseEnv)

		denseBytes := denseProtoSize(t, h)

		ratio := float64(denseBytes) / float64(len(sparseBytes))
		t.Logf("card=%d nz=%d sparse=%d dense=%d ratio=%.1fx",
			card, nz, len(sparseBytes), denseBytes, ratio)
		if len(sparseBytes) >= denseBytes {
			t.Fatalf("card=%d: sparse (%d) not smaller than dense (%d)", card, len(sparseBytes), denseBytes)
		}
		if ratio < 5.0 {
			t.Fatalf("card=%d: expected >=5x headroom, got %.1fx", card, ratio)
		}
	}

	// At/above crossover: encoder uses dense; size ~ raw registers + overhead.
	h := NewHyperLogLog()
	regs := h.Registers.AsMutSlice()
	for i := 0; i < SparseCrossoverNonZero+500 && i < len(regs); i++ {
		regs[i] = uint8((i % 50) + 1)
	}
	env, _ := h.SerializePortable()
	b, _ := proto.Marshal(env)
	t.Logf("high-card nz=%d dense=%d (raw regs=%d)", countNonZero(regs), len(b), HLLRegisterCount)
	if len(b) < HLLRegisterCount {
		t.Fatalf("dense payload %d unexpectedly below raw register count %d", len(b), HLLRegisterCount)
	}
}

// denseProtoSize marshals h forcing the DENSE encoding, to compare against the
// sparse size at the same register state.
func denseProtoSize(t *testing.T, h *HyperLogLog) int {
	t.Helper()
	state := &hllpb.HyperLogLogState{
		Variant:   hllpb.HLLVariant_HLL_VARIANT_DATAFUSION,
		Precision: HLLPrecision,
		Registers: append([]byte(nil), h.RegisterSlice()...),
	}
	env := hllEnvelope(state, 0.0)
	b, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("dense marshal: %v", err)
	}
	return len(b)
}

func assertRegistersEqual(t *testing.T, want, got []uint8) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("register length mismatch: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("register[%d] mismatch: want %d, got %d", i, want[i], got[i])
		}
	}
}
