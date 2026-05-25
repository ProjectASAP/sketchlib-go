package hll

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	hllpb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	"google.golang.org/protobuf/proto"
)

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
