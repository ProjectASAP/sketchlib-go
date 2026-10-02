package asapmsgpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// WrapperMagic is the 6-byte ASCII sentinel that opens every ASAP sketch binary.
var WrapperMagic = [6]byte{'A', 'S', 'A', 'P', 'v', '1'}

// WrapperVersion is the current envelope layout version stored immediately after WrapperMagic.
const WrapperVersion byte = 0x02

// EncodeWrapper prepends the ASAPv1 envelope to payload and returns the complete binary.
//
// kindID identifies the sketch type (1 byte for portable types, 2 bytes for
// native Rust types). The resulting layout is:
//
//	[ b"ASAPv1" | version:u8 | kind_id_len:u8 | kind_id:bytes | metadata_len:u32 | metadata:msgpack | payload ]
func EncodeWrapper(kindID []byte, payload []byte) []byte {
	metadata := encodeMetadata(metadataForKindID(kindID))
	out := make([]byte, 0, len(WrapperMagic)+1+1+len(kindID)+4+len(metadata)+len(payload))
	out = append(out, WrapperMagic[:]...)
	out = append(out, WrapperVersion)
	out = append(out, byte(len(kindID)))
	out = append(out, kindID...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(metadata)))
	out = append(out, metadata...)
	out = append(out, payload...)
	return out
}

// DecodeWrapper strips the ASAPv1 envelope from data and returns (kindID, payload).
// Returns an error on any structural mismatch.
func DecodeWrapper(data []byte) (kindID []byte, payload []byte, err error) {
	kindID, _, payload, err = DecodeWrapperWithMetadata(data)
	return kindID, payload, err
}

// DecodeWrapperWithMetadata strips the ASAPv1 envelope from data and returns
// (kindID, metadata, payload). Returns an error on any structural mismatch or
// unsupported metadata.
func DecodeWrapperWithMetadata(data []byte) (kindID []byte, metadata WrapperMetadata, payload []byte, err error) {
	magicLen := len(WrapperMagic)
	versionOffset := magicLen
	kindIDLenOffset := magicLen + 1
	kindIDOffset := magicLen + 2
	minLen := kindIDOffset + 1 + 4

	if len(data) < minLen {
		return nil, WrapperMetadata{}, nil, fmt.Errorf(
			"asapmsgpack: wrapper too short (%d bytes, need ≥%d)", len(data), minLen)
	}
	if !bytes.Equal(data[:magicLen], WrapperMagic[:]) {
		return nil, WrapperMetadata{}, nil, fmt.Errorf(
			"asapmsgpack: bad wrapper magic %q, expected \"ASAPv1\"", data[:magicLen])
	}
	if data[versionOffset] != WrapperVersion {
		return nil, WrapperMetadata{}, nil, fmt.Errorf(
			"asapmsgpack: unsupported wrapper version 0x%02x", data[versionOffset])
	}
	kindIDLen := int(data[kindIDLenOffset])
	metadataLenOffset := kindIDOffset + kindIDLen
	if len(data) < metadataLenOffset+4 {
		return nil, WrapperMetadata{}, nil, fmt.Errorf(
			"asapmsgpack: wrapper kind_id_len=%d but only %d bytes after offset %d",
			kindIDLen, len(data)-kindIDOffset, kindIDOffset)
	}
	metadataLen := int(binary.BigEndian.Uint32(data[metadataLenOffset : metadataLenOffset+4]))
	metadataStart := metadataLenOffset + 4
	metadataEnd := metadataStart + metadataLen
	if len(data) < metadataEnd {
		return nil, WrapperMetadata{}, nil, fmt.Errorf(
			"asapmsgpack: wrapper metadata_len=%d but only %d bytes after offset %d",
			metadataLen, len(data)-metadataStart, metadataStart)
	}
	kindID = data[kindIDOffset:metadataLenOffset]
	metadata, err = decodeMetadata(data[metadataStart:metadataEnd])
	if err != nil {
		return nil, WrapperMetadata{}, nil, fmt.Errorf("asapmsgpack: invalid wrapper metadata: %w", err)
	}
	if err := validateMetadataMatchesKindID(kindID, metadata); err != nil {
		return nil, WrapperMetadata{}, nil, err
	}
	return kindID, metadata, data[metadataEnd:], nil
}

// DecodePayload strips the ASAPv1 envelope and verifies the portable 1-byte
// kind ID before returning the MessagePack payload.
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
