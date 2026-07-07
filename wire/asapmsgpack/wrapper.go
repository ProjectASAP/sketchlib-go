package asapmsgpack

import (
	"bytes"
	"fmt"
)

// WrapperMagic is the 6-byte ASCII sentinel that opens every ASAP sketch binary.
var WrapperMagic = [6]byte{'A', 'S', 'A', 'P', 'v', '1'}

// WrapperVersion is the current envelope layout version stored immediately after WrapperMagic.
const WrapperVersion byte = 0x01

// EncodeWrapper prepends the ASAPv1 envelope to payload and returns the complete binary.
//
// kindID identifies the sketch type (1 byte for portable types, 2 bytes for
// native Rust types). The resulting layout is:
//
//	[ b"ASAPv1" | version:u8 | kind_id_len:u8 | kind_id:bytes | payload ]
func EncodeWrapper(kindID []byte, payload []byte) []byte {
	out := make([]byte, 0, len(WrapperMagic)+1+1+len(kindID)+len(payload))
	out = append(out, WrapperMagic[:]...)
	out = append(out, WrapperVersion)
	out = append(out, byte(len(kindID)))
	out = append(out, kindID...)
	out = append(out, payload...)
	return out
}

// DecodeWrapper strips the ASAPv1 envelope from data and returns (kindID, payload).
// Returns an error on any structural mismatch.
func DecodeWrapper(data []byte) (kindID []byte, payload []byte, err error) {
	magicLen := len(WrapperMagic)
	versionOffset := magicLen
	kindIDLenOffset := magicLen + 1
	kindIDOffset := magicLen + 2
	minLen := kindIDOffset + 1

	if len(data) < minLen {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper too short (%d bytes, need ≥%d)", len(data), minLen)
	}
	if !bytes.Equal(data[:magicLen], WrapperMagic[:]) {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: bad wrapper magic %q, expected \"ASAPv1\"", data[:magicLen])
	}
	if data[versionOffset] != WrapperVersion {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: unsupported wrapper version 0x%02x", data[versionOffset])
	}
	kindIDLen := int(data[kindIDLenOffset])
	headerEnd := kindIDOffset + kindIDLen
	if len(data) < headerEnd {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper kind_id_len=%d but only %d bytes after offset %d",
			kindIDLen, len(data)-kindIDOffset, kindIDOffset)
	}
	return data[kindIDOffset:headerEnd], data[headerEnd:], nil
}
