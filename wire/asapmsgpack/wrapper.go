package asapmsgpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// WrapperMagic is the 6-byte ASCII sentinel that opens every ASAP sketch binary.
var WrapperMagic = [6]byte{'A', 'S', 'A', 'P', 'v', '1'}

// WrapperVersion is the current envelope layout version stored immediately
// after WrapperMagic. This is ASAPv1 envelope layout `0x01`
// (asap_sketchlib/docs/asapv1_wire_format.md §1), which adds an explicit
// payload_len after metadata_len so the record is self-delimiting.
const WrapperVersion byte = 0x01

// EncodeWrapper assembles the ASAPv1 envelope around an already-encoded
// metadata block and payload. It is sketch-agnostic pure framing — it mirrors
// Rust's `message_pack_format::envelope::encode` and does NOT know the kind_id
// registry or the metadata schema; the caller supplies both blocks.
//
// kindID names the sketch algorithm (`[family, variant]`, 2 bytes for the
// ASAPv1-aligned HLL and Count-Min types; 1 byte for the not-yet-converted
// portable sketches). The resulting layout is:
//
//	[ b"ASAPv1" | version:u8 | kind_id_len:u8 | kind_id:bytes
//	            | metadata_len:u32_be | payload_len:u32_be
//	            | metadata:msgpack | payload:msgpack ]
func EncodeWrapper(kindID, metadata, payload []byte) []byte {
	out := make([]byte, 0,
		len(WrapperMagic)+1+1+len(kindID)+4+4+len(metadata)+len(payload))
	out = append(out, WrapperMagic[:]...)
	out = append(out, WrapperVersion)
	out = append(out, byte(len(kindID)))
	out = append(out, kindID...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(metadata)))
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, metadata...)
	out = append(out, payload...)
	return out
}

// SplitWrapper strips the ASAPv1 envelope and returns the three framed slices
// (kindID, metadata, payload). It validates only the magic, version and framing
// — like Rust's `envelope::split`, it does NOT validate kind_id against a
// registry or parse the metadata; each sketch's decoder does that.
func SplitWrapper(data []byte) (kindID, metadata, payload []byte, err error) {
	magicLen := len(WrapperMagic)
	versionOffset := magicLen
	kindIDLenOffset := magicLen + 1
	kindIDOffset := magicLen + 2
	minLen := kindIDOffset + 4 + 4

	if len(data) < minLen {
		return nil, nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper too short (%d bytes, need ≥%d)", len(data), minLen)
	}
	if !bytes.Equal(data[:magicLen], WrapperMagic[:]) {
		return nil, nil, nil, fmt.Errorf(
			"asapmsgpack: bad wrapper magic %q, expected \"ASAPv1\"", data[:magicLen])
	}
	if data[versionOffset] != WrapperVersion {
		return nil, nil, nil, fmt.Errorf(
			"asapmsgpack: unsupported wrapper version 0x%02x", data[versionOffset])
	}
	kindIDLen := int(data[kindIDLenOffset])
	lengthsOffset := kindIDOffset + kindIDLen
	if len(data) < lengthsOffset+8 {
		return nil, nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper truncated kind_id / length fields "+
				"(kind_id_len=%d, only %d bytes after offset %d)",
			kindIDLen, len(data)-kindIDOffset, kindIDOffset)
	}
	kindID = data[kindIDOffset:lengthsOffset]
	metadataLen := int(binary.BigEndian.Uint32(data[lengthsOffset : lengthsOffset+4]))
	payloadLen := int(binary.BigEndian.Uint32(data[lengthsOffset+4 : lengthsOffset+8]))
	metadataStart := lengthsOffset + 8
	payloadStart := metadataStart + metadataLen
	payloadEnd := payloadStart + payloadLen
	if payloadStart < metadataStart || payloadEnd < payloadStart || len(data) < payloadEnd {
		return nil, nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper truncated metadata/payload "+
				"(metadata_len=%d, payload_len=%d, have %d bytes after offset %d)",
			metadataLen, payloadLen, len(data)-metadataStart, metadataStart)
	}
	return kindID, data[metadataStart:payloadStart], data[payloadStart:payloadEnd], nil
}

// DecodeWrapper strips the ASAPv1 envelope and returns (kindID, payload),
// discarding the metadata block. Kept as a convenience for callers that only
// need to route on kind_id; sketch decoders that must validate the metadata
// use SplitWrapper directly.
func DecodeWrapper(data []byte) (kindID, payload []byte, err error) {
	kindID, _, payload, err = SplitWrapper(data)
	return kindID, payload, err
}

// DecodePayload strips the ASAPv1 envelope and verifies a 1-byte kind_id before
// returning the MessagePack payload. Used by the portable sketches that have
// not yet been converted to the 2-byte `[family, variant]` kind_id scheme.
func DecodePayload(data []byte, expectedKind byte) ([]byte, error) {
	kindID, payload, err := DecodeWrapper(data)
	if err != nil {
		return nil, err
	}
	if len(kindID) != 1 || kindID[0] != expectedKind {
		return nil, fmt.Errorf("kind_id mismatch: expected [0x%02x], got %x", expectedKind, kindID)
	}
	return payload, nil
}
