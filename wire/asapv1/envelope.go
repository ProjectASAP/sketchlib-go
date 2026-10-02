package asapv1

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Magic is the 6-byte sentinel that opens every envelope.
const Magic = "ASAPv1"

// Version is the envelope layout version.
const Version byte = 0x01

// headerMin is the envelope length with an empty kind_id and no blocks.
const headerMin = len(Magic) + 1 + 1 + 4 + 4

// Marshaler is implemented by every sketch type with an ASAPv1 encoding.
type Marshaler interface {
	MarshalASAPv1() ([]byte, error)
}

// Unmarshaler is implemented by a pointer to every sketch type with an ASAPv1
// encoding.
type Unmarshaler interface {
	UnmarshalASAPv1(b []byte) error
}

// Encode frames metadata and payload in an envelope tagged with kind.
func Encode(kind KindID, metadata, payload []byte) ([]byte, error) {
	if len(kind) > math.MaxUint8 {
		return nil, fmt.Errorf("asapv1: kind_id of %d bytes exceeds 255", len(kind))
	}
	if uint64(len(metadata)) > math.MaxUint32 || uint64(len(payload)) > math.MaxUint32 {
		return nil, fmt.Errorf("asapv1: block exceeds 4 GiB (metadata %d, payload %d bytes)",
			len(metadata), len(payload))
	}
	out := make([]byte, 0, headerMin+len(kind)+len(metadata)+len(payload))
	out = append(out, Magic...)
	out = append(out, Version, byte(len(kind)))
	out = append(out, kind...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(metadata)))
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, metadata...)
	out = append(out, payload...)
	return out, nil
}

// Split validates the magic, version and framing of an envelope and returns
// its kind_id, metadata and payload. Bytes past the payload are ignored. The
// returned slices alias b.
func Split(b []byte) (kind KindID, metadata, payload []byte, err error) {
	if len(b) < headerMin {
		return "", nil, nil, fmt.Errorf("asapv1: envelope too short (%d bytes, need at least %d)", len(b), headerMin)
	}
	if string(b[:len(Magic)]) != Magic {
		return "", nil, nil, fmt.Errorf("asapv1: bad magic %q", b[:len(Magic)])
	}
	if v := b[len(Magic)]; v != Version {
		return "", nil, nil, fmt.Errorf("asapv1: unsupported envelope version 0x%02x", v)
	}
	kindStart := len(Magic) + 2
	lengths := kindStart + int(b[len(Magic)+1])
	if len(b) < lengths+8 {
		return "", nil, nil, fmt.Errorf("asapv1: envelope truncated in kind_id or length fields")
	}
	metadataLen := uint64(binary.BigEndian.Uint32(b[lengths:]))
	payloadLen := uint64(binary.BigEndian.Uint32(b[lengths+4:]))
	metadataStart := uint64(lengths + 8)
	payloadStart := metadataStart + metadataLen
	payloadEnd := payloadStart + payloadLen
	if uint64(len(b)) < payloadEnd {
		return "", nil, nil, fmt.Errorf("asapv1: envelope truncated (metadata_len %d, payload_len %d, %d bytes follow the header)",
			metadataLen, payloadLen, uint64(len(b))-metadataStart)
	}
	return KindID(b[kindStart:lengths]), b[metadataStart:payloadStart], b[payloadStart:payloadEnd], nil
}

// SplitKind is Split, failing unless the envelope's kind_id is want.
func SplitKind(b []byte, want KindID) (metadata, payload []byte, err error) {
	kind, metadata, payload, err := Split(b)
	if err != nil {
		return nil, nil, err
	}
	if kind != want {
		return nil, nil, fmt.Errorf("asapv1: kind_id %v, want %v", kind, want)
	}
	return metadata, payload, nil
}

// Open is SplitKind followed by ReadMetadata on the metadata block and
// NewDecoder on the payload.
func Open(b []byte, want KindID) (*MetadataReader, *Decoder, error) {
	metadata, payload, err := SplitKind(b, want)
	if err != nil {
		return nil, nil, err
	}
	md, err := ReadMetadata(metadata)
	if err != nil {
		return nil, nil, err
	}
	return md, NewDecoder(payload), nil
}
