package asapmsgpack

import (
	"fmt"
	"reflect"

	"github.com/ProjectASAP/sketchlib-go/common"
)

const (
	HashProfileProjectASAPXXH3V1 = "projectasap.xxh3.seedlist.v1"
	HashAlgorithmXXH364128       = "xxh3_64_128"
	HashSeedDerivationIndexWrap  = "seed_list_index_wrap"
	HashInputEncodingASAPV1      = "projectasap.input.v1"

	matrixBaseSeedIndex         uint32 = 0
	hydraSeedIndex              uint32 = 6
	univmonBottomLayerSeedIndex uint32 = 19
	metadataFieldCount                 = 11
	nativeHydra                 byte   = 0x8d
	nativeUnivMon               byte   = 0x8e
	hasherDefaultXX             byte   = 0x01
	hasherUnknown               byte   = 0xff
)

// WrapperMetadata is the compact MessagePack metadata block carried by the
// ASAPv1 wrapper between kind_id and the sketch payload.
type WrapperMetadata struct {
	MetadataVersion             uint8
	HashSpecPresent             bool
	HashProfileID               string
	HashAlgorithm               string
	SeedList                    []uint64
	CanonicalSeedIndex          uint32
	MatrixSeedIndex             uint32
	HydraSeedIndex              uint32
	UnivmonBottomLayerSeedIndex uint32
	SeedDerivation              string
	InputEncoding               string
}

// StandardHashMetadata returns the registered ProjectASAP xxh3 seed-list
// profile used by portable sketches and native DefaultXxHasher sketches.
func StandardHashMetadata() WrapperMetadata {
	return WrapperMetadata{
		MetadataVersion:             1,
		HashSpecPresent:             true,
		HashProfileID:               HashProfileProjectASAPXXH3V1,
		HashAlgorithm:               HashAlgorithmXXH364128,
		SeedList:                    common.SeedList(),
		CanonicalSeedIndex:          uint32(common.CanonicalHashSeed),
		MatrixSeedIndex:             matrixBaseSeedIndex,
		HydraSeedIndex:              hydraSeedIndex,
		UnivmonBottomLayerSeedIndex: univmonBottomLayerSeedIndex,
		SeedDerivation:              HashSeedDerivationIndexWrap,
		InputEncoding:               HashInputEncodingASAPV1,
	}
}

func noHashMetadata() WrapperMetadata {
	return WrapperMetadata{
		MetadataVersion: 1,
		SeedList:        []uint64{},
	}
}

func metadataForKindID(kindID []byte) WrapperMetadata {
	switch {
	case len(kindID) == 1 && kindID[0] >= MagicHLL && kindID[0] <= MagicDeltaResult:
		return StandardHashMetadata()
	case len(kindID) == 2 && kindID[1] == hasherDefaultXX:
		return StandardHashMetadata()
	case len(kindID) == 2 &&
		(kindID[0] == nativeHydra || kindID[0] == nativeUnivMon) &&
		kindID[1] == hasherUnknown:
		return StandardHashMetadata()
	default:
		return noHashMetadata()
	}
}

func encodeMetadata(metadata WrapperMetadata) []byte {
	enc := newEncoder()
	enc.writeArrayLen(metadataFieldCount)
	enc.writeUint(uint64(metadata.MetadataVersion))
	enc.writeBool(metadata.HashSpecPresent)
	enc.writeString(metadata.HashProfileID)
	enc.writeString(metadata.HashAlgorithm)
	enc.writeArrayLen(len(metadata.SeedList))
	for _, seed := range metadata.SeedList {
		enc.writeUint(seed)
	}
	enc.writeUint(uint64(metadata.CanonicalSeedIndex))
	enc.writeUint(uint64(metadata.MatrixSeedIndex))
	enc.writeUint(uint64(metadata.HydraSeedIndex))
	enc.writeUint(uint64(metadata.UnivmonBottomLayerSeedIndex))
	enc.writeString(metadata.SeedDerivation)
	enc.writeString(metadata.InputEncoding)
	return enc.bytes()
}

func decodeMetadata(buf []byte) (WrapperMetadata, error) {
	dec := newDecoder(buf)
	fields, err := dec.readArrayLen()
	if err != nil {
		return WrapperMetadata{}, err
	}
	if fields != metadataFieldCount {
		return WrapperMetadata{}, fmt.Errorf(
			"asapmsgpack: wrapper metadata has %d fields, want %d",
			fields, metadataFieldCount)
	}

	version, err := dec.readUint()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: metadata_version: %w", err)
	}
	if version > 0xff {
		return WrapperMetadata{}, fmt.Errorf(
			"asapmsgpack: metadata_version %d out of u8 range", version)
	}
	hashSpecPresent, err := dec.readBool()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: hash_spec_present: %w", err)
	}
	hashProfileID, err := dec.readString()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: hash_profile_id: %w", err)
	}
	hashAlgorithm, err := dec.readString()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: hash_algorithm: %w", err)
	}

	seedCount, err := dec.readArrayLen()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: seed_list: %w", err)
	}
	standardSeedCount := len(common.SeedList())
	if seedCount != 0 && seedCount != standardSeedCount {
		return WrapperMetadata{}, fmt.Errorf(
			"asapmsgpack: seed_list has %d entries, want 0 or %d",
			seedCount, standardSeedCount)
	}
	seedList := make([]uint64, seedCount)
	for i := range seedList {
		seed, err := dec.readUint()
		if err != nil {
			return WrapperMetadata{}, fmt.Errorf("asapmsgpack: seed_list[%d]: %w", i, err)
		}
		seedList[i] = seed
	}

	canonicalSeedIndex, err := readU32MetadataField(dec, "canonical_seed_index")
	if err != nil {
		return WrapperMetadata{}, err
	}
	matrixSeedIndex, err := readU32MetadataField(dec, "matrix_seed_index")
	if err != nil {
		return WrapperMetadata{}, err
	}
	hydraSeedIndex, err := readU32MetadataField(dec, "hydra_seed_index")
	if err != nil {
		return WrapperMetadata{}, err
	}
	univmonBottomLayerSeedIndex, err := readU32MetadataField(dec, "univmon_bottom_layer_seed_index")
	if err != nil {
		return WrapperMetadata{}, err
	}

	seedDerivation, err := dec.readString()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: seed_derivation: %w", err)
	}
	inputEncoding, err := dec.readString()
	if err != nil {
		return WrapperMetadata{}, fmt.Errorf("asapmsgpack: input_encoding: %w", err)
	}
	if err := dec.done(); err != nil {
		return WrapperMetadata{}, err
	}

	metadata := WrapperMetadata{
		MetadataVersion:             uint8(version),
		HashSpecPresent:             hashSpecPresent,
		HashProfileID:               hashProfileID,
		HashAlgorithm:               hashAlgorithm,
		SeedList:                    seedList,
		CanonicalSeedIndex:          canonicalSeedIndex,
		MatrixSeedIndex:             matrixSeedIndex,
		HydraSeedIndex:              hydraSeedIndex,
		UnivmonBottomLayerSeedIndex: univmonBottomLayerSeedIndex,
		SeedDerivation:              seedDerivation,
		InputEncoding:               inputEncoding,
	}
	return metadata, metadata.validate()
}

func readU32MetadataField(dec *decoder, name string) (uint32, error) {
	value, err := dec.readUint()
	if err != nil {
		return 0, fmt.Errorf("asapmsgpack: %s: %w", name, err)
	}
	if value > 0xffffffff {
		return 0, fmt.Errorf("asapmsgpack: %s %d out of u32 range", name, value)
	}
	return uint32(value), nil
}

func (m WrapperMetadata) validate() error {
	if m.MetadataVersion != 1 {
		return fmt.Errorf(
			"asapmsgpack: unsupported wrapper metadata_version %d", m.MetadataVersion)
	}
	if !m.HashSpecPresent {
		if !reflect.DeepEqual(m, noHashMetadata()) {
			return fmt.Errorf("asapmsgpack: no-hash wrapper metadata mismatch")
		}
		return nil
	}
	if !reflect.DeepEqual(m, StandardHashMetadata()) {
		return fmt.Errorf("asapmsgpack: hash wrapper metadata mismatch")
	}
	return nil
}

func validateMetadataMatchesKindID(kindID []byte, metadata WrapperMetadata) error {
	if !reflect.DeepEqual(metadata, metadataForKindID(kindID)) {
		return fmt.Errorf("asapmsgpack: wrapper metadata does not match kind_id %x", kindID)
	}
	return nil
}
