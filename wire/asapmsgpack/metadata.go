package asapmsgpack

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// ASAPv1 metadata (asap_sketchlib/docs/asapv1_wire_format.md §2). The metadata
// block is a MessagePack **map keyed by field name** (rmp_serde `to_vec_named`)
// carrying the hash spec (how keys were hashed) plus the per-sketch structural
// params needed to interpret the payload. Two consequences drive this code:
//
//   - **Canonical key order** — encoders MUST write keys in the order the Rust
//     metadata structs declare them (which is what `to_vec_named` emits and what
//     §2/§5 fix). The order is mirrored exactly below.
//   - **Fail closed / deny unknown fields** — decoders reject any key outside the
//     fixed set for the sketch, any duplicate, and any hash-spec value that does
//     not match the standard ProjectASAP profile Go is prepared to reproduce.
const (
	// HashProfileProjectASAPXXH3V1 is the stable global id of the standard
	// ProjectASAP hash profile.
	HashProfileProjectASAPXXH3V1 = "projectasap.xxh3.seedlist.v1"
	// HashAlgorithmXXH364128 identifies the XXH3 64/128 hash algorithm.
	HashAlgorithmXXH364128 = "xxh3_64_128"
	// HashSeedDerivationIndexWrap identifies the seed-index-wrap derivation.
	HashSeedDerivationIndexWrap = "seed_list_index_wrap"
	// HashInputEncodingASAPV1 identifies the ProjectASAP input encoding.
	HashInputEncodingASAPV1 = "projectasap.input.v1"

	matrixSeedIndex uint32 = 0

	// Metadata field-name keys (canonical order within each sketch's map).
	keyMetadataVersion    = "metadata_version"
	keyHashProfileID      = "hash_profile_id"
	keyHashAlgorithm      = "hash_algorithm"
	keySeedDerivation     = "seed_derivation"
	keyInputEncoding      = "input_encoding"
	keySeedList           = "seed_list"
	keyCanonicalSeedIndex = "canonical_seed_index"
	keyMatrixSeedIndex    = "matrix_seed_index"
	keyPrecision          = "precision"
	keyCounterType        = "counter_type"
	keyMode               = "mode"

	metadataVersionV1 = 1
)

// writeHashSpecPairs writes the six shared hash-spec key/value pairs, in
// canonical order, into enc. Every ASAPv1 sketch metadata map opens with these.
func writeHashSpecPairs(enc *encoder) {
	enc.writeString(keyMetadataVersion)
	enc.writeUint(metadataVersionV1)
	enc.writeString(keyHashProfileID)
	enc.writeString(HashProfileProjectASAPXXH3V1)
	enc.writeString(keyHashAlgorithm)
	enc.writeString(HashAlgorithmXXH364128)
	enc.writeString(keySeedDerivation)
	enc.writeString(HashSeedDerivationIndexWrap)
	enc.writeString(keyInputEncoding)
	enc.writeString(HashInputEncodingASAPV1)
	enc.writeString(keySeedList)
	seeds := common.SeedList()
	enc.writeArrayLen(len(seeds))
	for _, seed := range seeds {
		enc.writeUint(seed)
	}
}

// encodeHLLMetadata builds the HLL descriptor metadata map (§2): the shared
// hash spec plus the HLL-specific `canonical_seed_index` and `precision`.
// Mirrors Rust `HllMetadata` field order (8 keys).
func encodeHLLMetadata(precision uint32) []byte {
	enc := newEncoder()
	enc.writeMapLen(8)
	writeHashSpecPairs(enc)
	enc.writeString(keyCanonicalSeedIndex)
	enc.writeUint(uint64(common.CanonicalHashSeed))
	enc.writeString(keyPrecision)
	enc.writeUint(uint64(precision))
	return enc.bytes()
}

// encodeCMSMetadata builds the Count-Min descriptor metadata map (§2): the
// shared hash spec plus `matrix_seed_index`, `counter_type` and `mode`.
// Mirrors Rust `CmsMetadata` field order (9 keys).
func encodeCMSMetadata(counterType, mode string) []byte {
	enc := newEncoder()
	enc.writeMapLen(9)
	writeHashSpecPairs(enc)
	enc.writeString(keyMatrixSeedIndex)
	enc.writeUint(uint64(matrixSeedIndex))
	enc.writeString(keyCounterType)
	enc.writeString(counterType)
	enc.writeString(keyMode)
	enc.writeString(mode)
	return enc.bytes()
}

// encodeHashSpecMetadata builds a hash-spec-only metadata map (the six shared
// keys, no structural params). Used by the portable sketches that have not yet
// had ASAPv1 payloads / structural params designed, so their envelope is still
// well-formed and self-describing.
func encodeHashSpecMetadata() []byte {
	enc := newEncoder()
	enc.writeMapLen(6)
	writeHashSpecPairs(enc)
	return enc.bytes()
}

// StandardHashMetadata returns the encoded hash-spec-only metadata map (the six
// shared keys). Portable sketches that have not yet had ASAPv1 structural params
// designed embed this so their envelope stays well-formed and self-describing.
func StandardHashMetadata() []byte { return encodeHashSpecMetadata() }

// parsedMetadata holds the decoded key/value pairs of a metadata map along with
// per-key presence, so validation can be order-independent and fail closed.
type parsedMetadata struct {
	seen map[string]bool

	metadataVersion    uint64
	hashProfileID      string
	hashAlgorithm      string
	seedDerivation     string
	inputEncoding      string
	seedList           []uint64
	canonicalSeedIndex uint64
	matrixSeedIndex    uint64
	precision          uint64
	counterType        string
	mode               string
}

// decodeMetadataMap reads the metadata map, dispatching each key to the correct
// typed reader. allowed lists the keys valid for this sketch; any key outside it
// (or any duplicate) is rejected (deny_unknown_fields / fail closed).
func decodeMetadataMap(buf []byte, allowed map[string]bool) (parsedMetadata, error) {
	dec := newDecoder(buf)
	n, err := dec.readMapLen()
	if err != nil {
		return parsedMetadata{}, err
	}
	m := parsedMetadata{seen: make(map[string]bool, n)}
	for i := 0; i < n; i++ {
		key, err := dec.readString()
		if err != nil {
			return parsedMetadata{}, fmt.Errorf("asapmsgpack: metadata key: %w", err)
		}
		if !allowed[key] {
			return parsedMetadata{}, fmt.Errorf("asapmsgpack: unexpected metadata key %q", key)
		}
		if m.seen[key] {
			return parsedMetadata{}, fmt.Errorf("asapmsgpack: duplicate metadata key %q", key)
		}
		m.seen[key] = true

		switch key {
		case keyMetadataVersion:
			m.metadataVersion, err = dec.readUint()
		case keyHashProfileID:
			m.hashProfileID, err = dec.readString()
		case keyHashAlgorithm:
			m.hashAlgorithm, err = dec.readString()
		case keySeedDerivation:
			m.seedDerivation, err = dec.readString()
		case keyInputEncoding:
			m.inputEncoding, err = dec.readString()
		case keySeedList:
			m.seedList, err = readSeedList(dec)
		case keyCanonicalSeedIndex:
			m.canonicalSeedIndex, err = dec.readUint()
		case keyMatrixSeedIndex:
			m.matrixSeedIndex, err = dec.readUint()
		case keyPrecision:
			m.precision, err = dec.readUint()
		case keyCounterType:
			m.counterType, err = dec.readString()
		case keyMode:
			m.mode, err = dec.readString()
		default:
			// Unreachable: allowed keys are a subset of the cases above.
			err = fmt.Errorf("asapmsgpack: unhandled metadata key %q", key)
		}
		if err != nil {
			return parsedMetadata{}, fmt.Errorf("asapmsgpack: metadata %q: %w", key, err)
		}
	}
	if err := dec.done(); err != nil {
		return parsedMetadata{}, err
	}
	return m, nil
}

func readSeedList(dec *decoder) ([]uint64, error) {
	n, err := dec.readArrayLen()
	if err != nil {
		return nil, err
	}
	out := make([]uint64, n)
	for i := range out {
		if out[i], err = dec.readUint(); err != nil {
			return nil, fmt.Errorf("seed[%d]: %w", i, err)
		}
	}
	return out, nil
}

// validateHashSpec fails closed unless every hash-spec field equals the standard
// ProjectASAP profile Go is prepared to reproduce (§2 validation, cross-language
// contract). A sketch is only mergeable/queryable if its hash spec matches.
func (m parsedMetadata) validateHashSpec(required ...string) error {
	for _, key := range append([]string{
		keyMetadataVersion, keyHashProfileID, keyHashAlgorithm,
		keySeedDerivation, keyInputEncoding, keySeedList,
	}, required...) {
		if !m.seen[key] {
			return fmt.Errorf("asapmsgpack: metadata missing required key %q", key)
		}
	}
	if m.metadataVersion != metadataVersionV1 {
		return fmt.Errorf("asapmsgpack: unsupported metadata_version %d", m.metadataVersion)
	}
	if m.hashProfileID != HashProfileProjectASAPXXH3V1 {
		return fmt.Errorf("asapmsgpack: unexpected hash_profile_id %q", m.hashProfileID)
	}
	if m.hashAlgorithm != HashAlgorithmXXH364128 {
		return fmt.Errorf("asapmsgpack: unexpected hash_algorithm %q", m.hashAlgorithm)
	}
	if m.seedDerivation != HashSeedDerivationIndexWrap {
		return fmt.Errorf("asapmsgpack: unexpected seed_derivation %q", m.seedDerivation)
	}
	if m.inputEncoding != HashInputEncodingASAPV1 {
		return fmt.Errorf("asapmsgpack: unexpected input_encoding %q", m.inputEncoding)
	}
	want := common.SeedList()
	if len(m.seedList) != len(want) {
		return fmt.Errorf("asapmsgpack: seed_list has %d entries, want %d", len(m.seedList), len(want))
	}
	for i := range want {
		if m.seedList[i] != want[i] {
			return fmt.Errorf("asapmsgpack: seed_list[%d]=%#x, want %#x", i, m.seedList[i], want[i])
		}
	}
	return nil
}

// decodeHLLMetadata validates the hash spec and returns the HLL precision.
func decodeHLLMetadata(buf []byte) (precision uint32, err error) {
	m, err := decodeMetadataMap(buf, map[string]bool{
		keyMetadataVersion: true, keyHashProfileID: true, keyHashAlgorithm: true,
		keySeedDerivation: true, keyInputEncoding: true, keySeedList: true,
		keyCanonicalSeedIndex: true, keyPrecision: true,
	})
	if err != nil {
		return 0, err
	}
	if err := m.validateHashSpec(keyCanonicalSeedIndex, keyPrecision); err != nil {
		return 0, err
	}
	if m.canonicalSeedIndex != uint64(common.CanonicalHashSeed) {
		return 0, fmt.Errorf("asapmsgpack: unexpected canonical_seed_index %d", m.canonicalSeedIndex)
	}
	if m.precision > 0xffffffff {
		return 0, fmt.Errorf("asapmsgpack: precision %d out of u32 range", m.precision)
	}
	return uint32(m.precision), nil
}

// decodeCMSMetadata validates the hash spec and returns the Count-Min structural
// params (counter type and column-derivation mode).
func decodeCMSMetadata(buf []byte) (counterType, mode string, err error) {
	m, err := decodeMetadataMap(buf, map[string]bool{
		keyMetadataVersion: true, keyHashProfileID: true, keyHashAlgorithm: true,
		keySeedDerivation: true, keyInputEncoding: true, keySeedList: true,
		keyMatrixSeedIndex: true, keyCounterType: true, keyMode: true,
	})
	if err != nil {
		return "", "", err
	}
	if err := m.validateHashSpec(keyMatrixSeedIndex, keyCounterType, keyMode); err != nil {
		return "", "", err
	}
	if m.matrixSeedIndex != uint64(matrixSeedIndex) {
		return "", "", fmt.Errorf("asapmsgpack: unexpected matrix_seed_index %d", m.matrixSeedIndex)
	}
	return m.counterType, m.mode, nil
}
