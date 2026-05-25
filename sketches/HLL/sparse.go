package hll

import (
	"encoding/binary"
	"fmt"

	hllpb "github.com/ProjectASAP/sketchlib-go/proto/hll"
)

// SparseCrossoverNonZero is the dense/sparse crossover, expressed as a count of
// non-zero registers. At or above this threshold the encoder emits the DENSE
// `registers` byte array (tag 3); below it the encoder emits the SPARSE
// `registers_sparse` message (tag 7).
//
// Rationale (p=14, 16384 registers):
//   - Dense full state is 16384 bytes (1 byte/register).
//   - Sparse costs ~2–3 bytes per non-zero register: a uvarint index-delta
//     (1–2 bytes for sparse cardinality) plus a 1-byte value uvarint (register
//     values are 1..=Q+1 = 1..=51, always one varint byte).
//   - Sparse therefore beats the 16384-byte dense array while
//     nonzero * ~2.7 < 16384, i.e. roughly below ~6000 non-zero registers.
//
// 6000 is the conservative crossover from the holistic-compression audit
// (ASAPQuery-backend PR #331 §2.1 P1): below it sparse is always smaller; above
// it dense is at most ~16384 bytes and never larger than a sparse blow-up.
const SparseCrossoverNonZero = 6000

// countNonZero returns the number of non-zero registers in regs.
func countNonZero(regs []uint8) int {
	n := 0
	for _, v := range regs {
		if v != 0 {
			n++
		}
	}
	return n
}

// encodeSparseRegisters packs the non-zero registers of regs into an
// HLLSparseRegisters message using the (index_delta, value) uvarint layout
// documented on the proto message. regs is the full dense register array.
func encodeSparseRegisters(regs []uint8) *hllpb.HLLSparseRegisters {
	// Worst-case 2 bytes (index delta) + 1 byte (value) per non-zero register.
	packed := make([]byte, 0, len(regs))
	var buf [binary.MaxVarintLen64]byte

	prev := 0
	for i, v := range regs {
		if v == 0 {
			continue
		}
		delta := i - prev
		prev = i
		n := binary.PutUvarint(buf[:], uint64(delta))
		packed = append(packed, buf[:n]...)
		n = binary.PutUvarint(buf[:], uint64(v))
		packed = append(packed, buf[:n]...)
	}

	return &hllpb.HLLSparseRegisters{
		NumRegisters: uint32(len(regs)),
		Packed:       packed,
	}
}

// decodeSparseRegisters reconstructs the dense register array from a sparse
// message. The returned slice has length sp.NumRegisters with all non-zero
// registers restored at their original indices. Mirrors the Rust decoder in
// asap_sketchlib (message_pack_format/portable/hll.rs).
func decodeSparseRegisters(sp *hllpb.HLLSparseRegisters) ([]uint8, error) {
	n := int(sp.GetNumRegisters())
	if n < 0 {
		return nil, fmt.Errorf("hll: sparse num_registers %d invalid", n)
	}
	regs := make([]uint8, n)

	packed := sp.GetPacked()
	prev := 0
	first := true
	for off := 0; off < len(packed); {
		delta, m := binary.Uvarint(packed[off:])
		if m <= 0 {
			return nil, fmt.Errorf("hll: sparse index-delta varint corrupt at offset %d", off)
		}
		off += m

		val, m := binary.Uvarint(packed[off:])
		if m <= 0 {
			return nil, fmt.Errorf("hll: sparse value varint corrupt at offset %d", off)
		}
		off += m

		idx := prev + int(delta)
		// The very first delta is an absolute index (prev starts at 0); every
		// subsequent delta must be strictly positive so indices stay sorted and
		// unique.
		if !first && delta == 0 {
			return nil, fmt.Errorf("hll: sparse non-increasing index at idx %d", idx)
		}
		first = false
		if idx < 0 || idx >= n {
			return nil, fmt.Errorf("hll: sparse index %d out of range [0,%d)", idx, n)
		}
		if val > 0xff {
			return nil, fmt.Errorf("hll: sparse value %d exceeds u8 at idx %d", val, idx)
		}
		regs[idx] = uint8(val)
		prev = idx
	}
	return regs, nil
}
