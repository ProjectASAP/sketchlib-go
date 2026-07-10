package asapmsgpack

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// TestEncodeSplitWrapperRoundTrip pins the ASAPv1 envelope framing (§1): magic,
// version 0x01, kind_id_len, kind_id, metadata_len:u32be, payload_len:u32be.
func TestEncodeSplitWrapperRoundTrip(t *testing.T) {
	kindID := HLLKindErtlMLE
	metadata := []byte("meta-block")
	payload := []byte("payload-bytes")

	enc := EncodeWrapper(kindID, metadata, payload)

	if !bytes.HasPrefix(enc, WrapperMagic[:]) {
		t.Fatalf("missing magic: %x", enc[:6])
	}
	if enc[6] != WrapperVersion {
		t.Fatalf("version: got 0x%02x, want 0x01", enc[6])
	}
	if enc[7] != 2 {
		t.Fatalf("kind_id_len: got %d, want 2", enc[7])
	}
	if !bytes.Equal(enc[8:10], kindID) {
		t.Fatalf("kind_id: got %x, want %x", enc[8:10], kindID)
	}
	if got := binary.BigEndian.Uint32(enc[10:14]); got != uint32(len(metadata)) {
		t.Fatalf("metadata_len: got %d, want %d", got, len(metadata))
	}
	if got := binary.BigEndian.Uint32(enc[14:18]); got != uint32(len(payload)) {
		t.Fatalf("payload_len: got %d, want %d", got, len(payload))
	}

	gotKind, gotMeta, gotPayload, err := SplitWrapper(enc)
	if err != nil {
		t.Fatalf("SplitWrapper: %v", err)
	}
	if !bytes.Equal(gotKind, kindID) || !bytes.Equal(gotMeta, metadata) || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("split mismatch: %x / %x / %x", gotKind, gotMeta, gotPayload)
	}
}

func TestSplitWrapperRejectsBadMagicVersionAndTruncation(t *testing.T) {
	good := EncodeWrapper([]byte{0x01, 0x01}, []byte("m"), []byte("p"))

	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, _, _, err := SplitWrapper(bad); err == nil {
		t.Fatal("expected bad-magic rejection")
	}

	bad = append([]byte(nil), good...)
	bad[6] = 0x02 // old envelope version
	if _, _, _, err := SplitWrapper(bad); err == nil {
		t.Fatal("expected version rejection")
	}

	if _, _, _, err := SplitWrapper(good[:len(good)-1]); err == nil {
		t.Fatal("expected truncation rejection")
	}
	if _, _, _, err := SplitWrapper(good[:3]); err == nil {
		t.Fatal("expected short-buffer rejection")
	}
}

func TestDecodePayloadChecksOneByteKind(t *testing.T) {
	payload := []byte{0x91, 0x2a}
	enc := EncodeWrapper([]byte{MagicDDSketch}, StandardHashMetadata(), payload)

	got, err := DecodePayload(enc, MagicDDSketch)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload: got %x, want %x", got, payload)
	}
	if _, err := DecodePayload(enc, MagicCountSketch); err == nil {
		t.Fatal("expected kind_id mismatch")
	}
}

func TestHLLMetadataRoundTripAndValidation(t *testing.T) {
	meta := encodeHLLMetadata(12)
	precision, err := decodeHLLMetadata(meta)
	if err != nil {
		t.Fatalf("decodeHLLMetadata: %v", err)
	}
	if precision != 12 {
		t.Fatalf("precision: got %d, want 12", precision)
	}
}

func TestCMSMetadataRoundTripAndValidation(t *testing.T) {
	meta := encodeCMSMetadata(CMSCounterF64, CMSModeFast)
	counterType, mode, err := decodeCMSMetadata(meta)
	if err != nil {
		t.Fatalf("decodeCMSMetadata: %v", err)
	}
	if counterType != CMSCounterF64 || mode != CMSModeFast {
		t.Fatalf("got %q/%q, want f64/fast", counterType, mode)
	}
}

// TestMetadataRejectsUnknownKey pins the deny_unknown_fields / fail-closed rule
// (§2/§5): a key outside the HLL field set must be rejected.
func TestMetadataRejectsUnknownKey(t *testing.T) {
	enc := newEncoder()
	enc.writeMapLen(9) // 8 valid HLL keys + 1 bogus
	writeHashSpecPairs(enc)
	enc.writeString(keyCanonicalSeedIndex)
	enc.writeUint(uint64(common.CanonicalHashSeed))
	enc.writeString(keyPrecision)
	enc.writeUint(12)
	enc.writeString("bogus_field")
	enc.writeUint(7)

	if _, err := decodeHLLMetadata(enc.bytes()); err == nil {
		t.Fatal("expected unknown-key rejection")
	} else if !strings.Contains(err.Error(), "unexpected metadata key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMetadataRejectsWrongHashProfile pins that a hash spec Go cannot reproduce
// is rejected (§2 validation, fail closed).
func TestMetadataRejectsWrongHashProfile(t *testing.T) {
	enc := newEncoder()
	enc.writeMapLen(8)
	enc.writeString(keyMetadataVersion)
	enc.writeUint(1)
	enc.writeString(keyHashProfileID)
	enc.writeString("some.other.profile") // wrong
	enc.writeString(keyHashAlgorithm)
	enc.writeString(HashAlgorithmXXH364128)
	enc.writeString(keySeedDerivation)
	enc.writeString(HashSeedDerivationIndexWrap)
	enc.writeString(keyInputEncoding)
	enc.writeString(HashInputEncodingASAPV1)
	enc.writeString(keySeedList)
	seeds := common.SeedList()
	enc.writeArrayLen(len(seeds))
	for _, s := range seeds {
		enc.writeUint(s)
	}
	enc.writeString(keyCanonicalSeedIndex)
	enc.writeUint(uint64(common.CanonicalHashSeed))
	enc.writeString(keyPrecision)
	enc.writeUint(12)

	if _, err := decodeHLLMetadata(enc.bytes()); err == nil {
		t.Fatal("expected hash_profile_id rejection")
	} else if !strings.Contains(err.Error(), "hash_profile_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHLLClassicP12Bytes hand-verifies the byte-level ASAPv1 rules (§1/§3.1/§5)
// for the smallest reproducible case: HLL Classic, precision 12, all-zero
// registers. It checks the envelope framing, the metadata map header + key
// order + integer widths, and the FULL payload bytes (positional array +
// registers-as-bin + minimal-width lengths). These are the rules Go controls;
// the seed_list body and full cross-language byte parity await shared goldens
// (a follow-up — see PR body).
func TestHLLClassicP12Bytes(t *testing.T) {
	const p = 12
	regs := make([]byte, 1<<p) // 4096 zero registers
	enc, err := MarshalHLLSketch(HLLSketchState{
		Variant:   HLLVariantRegular,
		Precision: p,
		Registers: regs,
	})
	if err != nil {
		t.Fatalf("MarshalHLLSketch: %v", err)
	}

	// --- envelope framing ---
	if !bytes.HasPrefix(enc, WrapperMagic[:]) {
		t.Fatalf("magic: %x", enc[:6])
	}
	if enc[6] != 0x01 {
		t.Fatalf("version: 0x%02x, want 0x01", enc[6])
	}
	if enc[7] != 2 || !bytes.Equal(enc[8:10], []byte{0x01, 0x01}) {
		t.Fatalf("kind_id: len=%d id=%x, want 2 / 0101", enc[7], enc[8:10])
	}
	metaLen := binary.BigEndian.Uint32(enc[10:14])
	payloadLen := binary.BigEndian.Uint32(enc[14:18])

	meta := enc[18 : 18+metaLen]
	payload := enc[18+metaLen : 18+metaLen+payloadLen]

	// --- metadata map: fixmap of 8, canonical key order ---
	if meta[0] != 0x88 {
		t.Fatalf("metadata map header: 0x%02x, want 0x88 (fixmap 8)", meta[0])
	}
	// First key: "metadata_version" (16 bytes) -> fixstr 0xb0; value 1 -> 0x01.
	wantHead := append([]byte{0xb0}, []byte(keyMetadataVersion)...)
	wantHead = append(wantHead, 0x01)
	if !bytes.HasPrefix(meta[1:], wantHead) {
		t.Fatalf("metadata head: got %x", meta[1:1+len(wantHead)])
	}
	// The canonical keys must all be present in declaration order.
	orderedKeys := []string{
		keyMetadataVersion, keyHashProfileID, keyHashAlgorithm, keySeedDerivation,
		keyInputEncoding, keySeedList, keyCanonicalSeedIndex, keyPrecision,
	}
	lastIdx := -1
	for _, k := range orderedKeys {
		idx := bytes.Index(meta, append([]byte{0xa0 | byte(len(k))}, []byte(k)...))
		if idx < 0 {
			t.Fatalf("metadata missing key %q", k)
		}
		if idx <= lastIdx {
			t.Fatalf("metadata key %q out of canonical order", k)
		}
		lastIdx = idx
	}
	// precision (12) must be encoded as a positive fixint 0x0c, minimal width.
	pIdx := bytes.Index(meta, append([]byte{0xa0 | byte(len(keyPrecision))}, []byte(keyPrecision)...))
	if got := meta[pIdx+1+len(keyPrecision)]; got != 0x0c {
		t.Fatalf("precision value: 0x%02x, want 0x0c (fixint 12)", got)
	}
	// seed_list must be an array16 header (20 > 15 elements): 0xdc 0x00 0x14.
	slIdx := bytes.Index(meta, append([]byte{0xa0 | byte(len(keySeedList))}, []byte(keySeedList)...))
	slHdr := meta[slIdx+1+len(keySeedList) : slIdx+1+len(keySeedList)+3]
	if !bytes.Equal(slHdr, []byte{0xdc, 0x00, 0x14}) {
		t.Fatalf("seed_list header: %x, want dc0014 (array16, 20)", slHdr)
	}

	// --- payload: [ registers:bin ] with registers = bin16 of 4096 zeros ---
	wantPayload := append([]byte{0x91, 0xc5, 0x10, 0x00}, make([]byte, 1<<p)...)
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("payload mismatch:\n  got  %x...\n  want array1+bin16(4096 zeros)", payload[:8])
	}
}
