package hll

import (
	"encoding/binary"
	"fmt"

	pb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	"google.golang.org/protobuf/proto"
)

// packDelta varint-packs the increased registers of d as (index_delta, value)
// pairs, ascending by index, using the exact layout of HLLSparseRegisters.packed
// (see sparse.go::encodeSparseRegisters). d.Updates is expected to be sorted by
// ascending Index, which ComputeRegisterDelta guarantees (it walks registers in
// index order).
func packDelta(d *RegisterDelta) []byte {
	// Worst-case 2 bytes (index delta) + 1 byte (value) per update.
	packed := make([]byte, 0, len(d.Updates)*3)
	var buf [binary.MaxVarintLen64]byte

	prev := uint32(0)
	for i := range d.Updates {
		u := &d.Updates[i]
		delta := u.Index - prev
		prev = u.Index
		n := binary.PutUvarint(buf[:], uint64(delta))
		packed = append(packed, buf[:n]...)
		n = binary.PutUvarint(buf[:], uint64(u.Value))
		packed = append(packed, buf[:n]...)
	}
	return packed
}

// unpackDelta decodes a varint-packed (index_delta, value) blob produced by
// packDelta back into a RegisterDelta. Mirrors sparse.go::decodeSparseRegisters
// (and the Rust decoder) but without a fixed register-count bound, since a delta
// is sized by its own contents.
func unpackDelta(packed []byte) (*RegisterDelta, error) {
	updates := make([]RegisterUpdate, 0, len(packed)/2)
	prev := uint32(0)
	first := true
	for off := 0; off < len(packed); {
		delta, m := binary.Uvarint(packed[off:])
		if m <= 0 {
			return nil, fmt.Errorf("hll: delta index-delta varint corrupt at offset %d", off)
		}
		off += m

		val, m := binary.Uvarint(packed[off:])
		if m <= 0 {
			return nil, fmt.Errorf("hll: delta value varint corrupt at offset %d", off)
		}
		off += m

		idx := prev + uint32(delta)
		// The first delta is an absolute index (prev starts at 0); every
		// subsequent delta must be strictly positive so indices stay sorted
		// and unique.
		if !first && delta == 0 {
			return nil, fmt.Errorf("hll: delta non-increasing index at idx %d", idx)
		}
		first = false
		if val > 0xff {
			return nil, fmt.Errorf("hll: delta value %d exceeds u8 at idx %d", val, idx)
		}
		updates = append(updates, RegisterUpdate{Index: idx, Value: uint8(val)})
		prev = idx
	}
	return &RegisterDelta{Updates: updates}, nil
}

// SerializeRegisterDelta converts a RegisterDelta to proto-encoded bytes in one
// pass. The increased registers are varint-packed as (index_delta, value) pairs
// (the same layout HLLSparseRegisters uses for the full sparse state) and
// carried in HLLDelta.packed_updates.
func SerializeRegisterDelta(d *RegisterDelta) ([]byte, error) {
	return proto.Marshal(&pb.HLLDelta{PackedUpdates: packDelta(d)})
}

// DeserializeRegisterDelta converts proto-encoded bytes to a RegisterDelta in
// one pass, unpacking the (index_delta, value) blob from HLLDelta.packed_updates.
func DeserializeRegisterDelta(data []byte) (*RegisterDelta, error) {
	var msg pb.HLLDelta
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return unpackDelta(msg.GetPackedUpdates())
}
