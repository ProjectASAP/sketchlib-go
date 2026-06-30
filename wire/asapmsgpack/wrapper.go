package asapmsgpack

import "fmt"

// WrapperMagic is the 4-byte ASCII sentinel that opens every ASAP sketch binary.
var WrapperMagic = [4]byte{'A', 'S', 'K', '1'}

// WrapperVersion is the current envelope layout version stored in byte 4.
const WrapperVersion byte = 0x01

// EncodeWrapper prepends the ASK1 envelope to payload and returns the complete binary.
//
// kindID identifies the sketch type (1 byte for portable types, 2 bytes for
// native Rust types). The resulting layout is:
//
//	[ b"ASK1" | version:u8 | kind_id_len:u8 | kind_id:bytes | payload ]
func EncodeWrapper(kindID []byte, payload []byte) []byte {
	out := make([]byte, 0, 4+1+1+len(kindID)+len(payload))
	out = append(out, WrapperMagic[:]...)
	out = append(out, WrapperVersion)
	out = append(out, byte(len(kindID)))
	out = append(out, kindID...)
	out = append(out, payload...)
	return out
}

// DecodeWrapper strips the ASK1 envelope from data and returns (kindID, payload).
// Returns an error on any structural mismatch.
func DecodeWrapper(data []byte) (kindID []byte, payload []byte, err error) {
	if len(data) < 7 {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper too short (%d bytes, need ≥7)", len(data))
	}
	if [4]byte(data[:4]) != WrapperMagic {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: bad wrapper magic %q, expected \"ASK1\"", data[:4])
	}
	if data[4] != WrapperVersion {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: unsupported wrapper version 0x%02x", data[4])
	}
	kindIDLen := int(data[5])
	headerEnd := 6 + kindIDLen
	if len(data) < headerEnd {
		return nil, nil, fmt.Errorf(
			"asapmsgpack: wrapper kind_id_len=%d but only %d bytes after offset 6",
			kindIDLen, len(data)-6)
	}
	return data[6:headerEnd], data[headerEnd:], nil
}
