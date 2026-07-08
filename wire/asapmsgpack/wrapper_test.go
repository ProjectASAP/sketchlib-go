package asapmsgpack

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func encodeWrapperWithMetadata(kindID []byte, metadata WrapperMetadata, payload []byte) []byte {
	metadataBytes := encodeMetadata(metadata)
	out := make([]byte, 0, len(WrapperMagic)+1+1+len(kindID)+4+len(metadataBytes)+len(payload))
	out = append(out, WrapperMagic[:]...)
	out = append(out, WrapperVersion)
	out = append(out, byte(len(kindID)))
	out = append(out, kindID...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(metadataBytes)))
	out = append(out, metadataBytes...)
	out = append(out, payload...)
	return out
}

func TestWrapperRecordsStandardHashMetadata(t *testing.T) {
	payload := []byte{0x91, 0x2a}
	encoded := EncodeWrapper([]byte{MagicCountMinSketch}, payload)

	kindID, metadata, decodedPayload, err := DecodeWrapperWithMetadata(encoded)
	if err != nil {
		t.Fatalf("DecodeWrapperWithMetadata: %v", err)
	}

	if !reflect.DeepEqual(kindID, []byte{MagicCountMinSketch}) {
		t.Fatalf("kindID: got %x", kindID)
	}
	if !reflect.DeepEqual(decodedPayload, payload) {
		t.Fatalf("payload: got %x, want %x", decodedPayload, payload)
	}
	if !reflect.DeepEqual(metadata, StandardHashMetadata()) {
		t.Fatalf("metadata mismatch\n  got  %+v\n  want %+v", metadata, StandardHashMetadata())
	}
	if !reflect.DeepEqual(metadata.SeedList, common.SeedList()) {
		t.Fatalf("seed list mismatch\n  got  %x\n  want %x", metadata.SeedList, common.SeedList())
	}
	if metadata.CanonicalSeedIndex != uint32(common.CanonicalHashSeed) {
		t.Fatalf("canonical seed index: got %d", metadata.CanonicalSeedIndex)
	}
}

func TestWrapperUnknownNativeHasherDoesNotClaimHashMetadata(t *testing.T) {
	payload := []byte{0x90}
	kindID := []byte{0x81, hasherUnknown}
	encoded := EncodeWrapper(kindID, payload)

	decodedKindID, metadata, decodedPayload, err := DecodeWrapperWithMetadata(encoded)
	if err != nil {
		t.Fatalf("DecodeWrapperWithMetadata: %v", err)
	}

	if !reflect.DeepEqual(decodedKindID, kindID) {
		t.Fatalf("kindID: got %x, want %x", decodedKindID, kindID)
	}
	if !reflect.DeepEqual(decodedPayload, payload) {
		t.Fatalf("payload: got %x, want %x", decodedPayload, payload)
	}
	if !reflect.DeepEqual(metadata, noHashMetadata()) {
		t.Fatalf("metadata mismatch\n  got  %+v\n  want %+v", metadata, noHashMetadata())
	}
}

func TestWrapperRejectsHashMetadataMismatch(t *testing.T) {
	payload := []byte{0x90}
	metadata := StandardHashMetadata()
	metadata.CanonicalSeedIndex++
	encoded := encodeWrapperWithMetadata([]byte{MagicCountSketch}, metadata, payload)

	_, _, err := DecodeWrapper(encoded)
	if err == nil {
		t.Fatal("expected metadata mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "hash wrapper metadata mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWrapperRejectsMetadataKindIDMismatch(t *testing.T) {
	payload := []byte{0x90}
	encoded := encodeWrapperWithMetadata([]byte{MagicCountSketch}, noHashMetadata(), payload)

	_, _, err := DecodeWrapper(encoded)
	if err == nil {
		t.Fatal("expected metadata/kind_id mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "metadata does not match kind_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMetadataRejectsUnexpectedSeedListLengthBeforeAllocation(t *testing.T) {
	enc := newEncoder()
	enc.writeArrayLen(metadataFieldCount)
	enc.writeUint(1)
	enc.writeBool(true)
	enc.writeString(HashProfileProjectASAPXXH3V1)
	enc.writeString(HashAlgorithmXXH364128)
	enc.buf = append(enc.buf, 0xdd, 0x7f, 0xff, 0xff, 0xff)

	_, err := decodeMetadata(enc.bytes())
	if err == nil {
		t.Fatal("expected seed_list length rejection, got nil")
	}
	if !strings.Contains(err.Error(), "seed_list has") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodePayloadIgnoresValidatedMetadata(t *testing.T) {
	payload := []byte{0x91, 0x2a}
	encoded := EncodeWrapper([]byte{MagicHLL}, payload)

	decodedPayload, err := DecodePayload(encoded, MagicHLL)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if !reflect.DeepEqual(decodedPayload, payload) {
		t.Fatalf("payload: got %x, want %x", decodedPayload, payload)
	}
}
