package asapv1

import (
	"fmt"
	"slices"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// HashProfile is the identified set of hash constants a sketch was built with.
// Its values form the hash-spec group of the metadata.
type HashProfile struct {
	ID                 string
	Algorithm          string
	SeedDerivation     string
	InputEncoding      string
	SeedList           []uint64
	CanonicalSeedIndex uint32
	MatrixSeedIndex    uint32
}

// StandardProfile returns the standard ProjectASAP hash profile, the one every
// sketch in this module hashes with.
func StandardProfile() HashProfile {
	return HashProfile{
		ID:                 "projectasap.xxh3.seedlist.v1",
		Algorithm:          "xxh3_64_128",
		SeedDerivation:     "seed_list_index_wrap",
		InputEncoding:      "projectasap.input.v1",
		SeedList:           common.SeedList(),
		CanonicalSeedIndex: common.CanonicalHashSeed,
		MatrixSeedIndex:    0,
	}
}

// SeedIndex selects the seed-index key a sketch's hash-spec group carries.
type SeedIndex int

const (
	// SeedIndexNone carries no seed-index key, for a sketch whose hash index is
	// fixed by its algorithm.
	SeedIndexNone SeedIndex = iota
	// SeedIndexCanonical carries canonical_seed_index.
	SeedIndexCanonical
	// SeedIndexMatrix carries matrix_seed_index.
	SeedIndexMatrix
)

func (p HashProfile) seedIndex(idx SeedIndex) (key string, value uint32, err error) {
	switch idx {
	case SeedIndexNone:
		return "", 0, nil
	case SeedIndexCanonical:
		return "canonical_seed_index", p.CanonicalSeedIndex, nil
	case SeedIndexMatrix:
		return "matrix_seed_index", p.MatrixSeedIndex, nil
	}
	return "", 0, fmt.Errorf("asapv1: unknown SeedIndex %d", idx)
}

// MetadataWriter builds a metadata map. Keys are written in call order, which
// must be the kind's canonical order. It keeps the first error, which Err
// reports.
type MetadataWriter struct {
	body Encoder
	n    int
}

// NewMetadataWriter returns a writer that has written metadata_version.
func NewMetadataWriter(version uint8) *MetadataWriter {
	w := &MetadataWriter{}
	w.Uint("metadata_version", uint64(version))
	return w
}

// Err returns the first error, or nil.
func (w *MetadataWriter) Err() error { return w.body.err }

// Field writes key and returns the Encoder, on which the caller writes exactly
// one value.
func (w *MetadataWriter) Field(key string) *Encoder {
	w.n++
	w.body.Str(key)
	return &w.body
}

// Uint writes a uint-family integer field.
func (w *MetadataWriter) Uint(key string, v uint64) { w.Field(key).Uint(v) }

// Int writes an integer field under the family/width rule.
func (w *MetadataWriter) Int(key string, v int64) { w.Field(key).Int(v) }

// Float64 writes a float64 field.
func (w *MetadataWriter) Float64(key string, v float64) { w.Field(key).Float64(v) }

// Bool writes a bool field.
func (w *MetadataWriter) Bool(key string, v bool) { w.Field(key).Bool(v) }

// Str writes a str field.
func (w *MetadataWriter) Str(key, v string) { w.Field(key).Str(v) }

// Strs writes an array-of-str field.
func (w *MetadataWriter) Strs(key string, v []string) { EncodeStrs(w.Field(key), v) }

// HashSpec writes the hash-spec group of p, followed by the seed-index key idx
// selects.
func (w *MetadataWriter) HashSpec(p HashProfile, idx SeedIndex) {
	w.Str("hash_profile_id", p.ID)
	w.Str("hash_algorithm", p.Algorithm)
	w.Str("seed_derivation", p.SeedDerivation)
	w.Str("input_encoding", p.InputEncoding)
	EncodeUints(w.Field("seed_list"), p.SeedList)
	key, v, err := p.seedIndex(idx)
	if err != nil {
		w.body.Fail(err)
	} else if key != "" {
		w.Uint(key, uint64(v))
	}
}

// Bytes returns the encoded map.
func (w *MetadataWriter) Bytes() []byte {
	e := NewEncoder()
	e.Map(w.n)
	return append(e.Bytes(), w.body.Bytes()...)
}

// MetadataReader reads a metadata map by key, in any order. Each accessor
// consumes its key. It keeps the first error: after a failure accessors return
// zero values, and Finish reports the error.
type MetadataReader struct {
	keys    []string
	values  map[string][]byte
	used    map[string]bool
	version uint8
	err     error
}

// ReadMetadata parses a metadata map and consumes its metadata_version, which
// must be present and fit uint8. Keys must be str and unique.
func ReadMetadata(b []byte) (*MetadataReader, error) {
	d := NewDecoder(b)
	n := d.Map()
	r := &MetadataReader{values: make(map[string][]byte, n), used: make(map[string]bool, n)}
	for range n {
		key := d.Str()
		start := d.pos
		d.Skip()
		if d.err != nil {
			break
		}
		if _, dup := r.values[key]; dup {
			return nil, fmt.Errorf("asapv1: duplicate metadata key %q", key)
		}
		r.keys = append(r.keys, key)
		r.values[key] = b[start:d.pos]
	}
	if err := d.Finish(); err != nil {
		return nil, fmt.Errorf("asapv1: metadata: %w", err)
	}
	if r.version = r.Uint8("metadata_version"); r.err != nil {
		return nil, r.err
	}
	return r, nil
}

// Version returns the metadata_version.
func (r *MetadataReader) Version() uint8 { return r.version }

// ExpectVersion fails unless the metadata_version is one of accepted.
func (r *MetadataReader) ExpectVersion(accepted ...uint8) {
	if r.err == nil && !slices.Contains(accepted, r.version) {
		r.fail(fmt.Errorf("asapv1: unsupported metadata_version %d, want one of %v", r.version, accepted))
	}
}

func (r *MetadataReader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// Has reports whether key is present, without consuming it.
func (r *MetadataReader) Has(key string) bool {
	_, ok := r.values[key]
	return ok
}

// Field consumes key and runs read on a Decoder over its value, which read
// must consume entirely. A missing key is an error.
func (r *MetadataReader) Field(key string, read func(d *Decoder)) {
	if r.err != nil {
		return
	}
	v, ok := r.values[key]
	if !ok {
		r.fail(fmt.Errorf("asapv1: metadata key %q missing", key))
		return
	}
	r.used[key] = true
	d := NewDecoder(v)
	read(d)
	if err := d.Finish(); err != nil {
		r.fail(fmt.Errorf("asapv1: metadata key %q: %w", key, err))
	}
}

// Uint8 consumes key as an integer that fits uint8.
func (r *MetadataReader) Uint8(key string) (v uint8) {
	r.Field(key, func(d *Decoder) { v = d.Uint8() })
	return v
}

// Uint32 consumes key as an integer that fits uint32.
func (r *MetadataReader) Uint32(key string) (v uint32) {
	r.Field(key, func(d *Decoder) { v = d.Uint32() })
	return v
}

// Uint64 consumes key as a non-negative integer.
func (r *MetadataReader) Uint64(key string) (v uint64) {
	r.Field(key, func(d *Decoder) { v = d.Uint() })
	return v
}

// Int64 consumes key as an integer that fits int64.
func (r *MetadataReader) Int64(key string) (v int64) {
	r.Field(key, func(d *Decoder) { v = d.Int() })
	return v
}

// Float64 consumes key as a float64.
func (r *MetadataReader) Float64(key string) (v float64) {
	r.Field(key, func(d *Decoder) { v = d.Float64() })
	return v
}

// Bool consumes key as a bool.
func (r *MetadataReader) Bool(key string) (v bool) {
	r.Field(key, func(d *Decoder) { v = d.Bool() })
	return v
}

// Str consumes key as a str.
func (r *MetadataReader) Str(key string) (v string) {
	r.Field(key, func(d *Decoder) { v = d.Str() })
	return v
}

// Strs consumes key as an array of str.
func (r *MetadataReader) Strs(key string) (v []string) {
	r.Field(key, func(d *Decoder) { v = DecodeStrs(d) })
	return v
}

// ExpectUint consumes key and fails unless it is the integer want.
func (r *MetadataReader) ExpectUint(key string, want uint64) {
	if got := r.Uint64(key); r.err == nil && got != want {
		r.fail(fmt.Errorf("asapv1: metadata %s = %d, want %d", key, got, want))
	}
}

// ExpectStr consumes key and fails unless it is the str want.
func (r *MetadataReader) ExpectStr(key, want string) {
	if got := r.Str(key); r.err == nil && got != want {
		r.fail(fmt.Errorf("asapv1: metadata %s = %q, want %q", key, got, want))
	}
}

// HashSpec consumes the hash-spec group and the seed-index key idx selects,
// failing unless every value equals p's.
func (r *MetadataReader) HashSpec(p HashProfile, idx SeedIndex) {
	r.ExpectStr("hash_profile_id", p.ID)
	r.ExpectStr("hash_algorithm", p.Algorithm)
	r.ExpectStr("seed_derivation", p.SeedDerivation)
	r.ExpectStr("input_encoding", p.InputEncoding)
	var seeds []uint64
	r.Field("seed_list", func(d *Decoder) { seeds = DecodeUints[uint64](d) })
	if r.err == nil && !slices.Equal(seeds, p.SeedList) {
		r.fail(fmt.Errorf("asapv1: metadata seed_list %#x, want %#x", seeds, p.SeedList))
	}
	key, v, err := p.seedIndex(idx)
	if err != nil {
		r.fail(err)
	} else if key != "" {
		r.ExpectUint(key, uint64(v))
	}
}

// Err returns the first error, or nil.
func (r *MetadataReader) Err() error { return r.err }

// Finish returns the first error, or an error naming the first key no accessor
// consumed.
func (r *MetadataReader) Finish() error {
	if r.err != nil {
		return r.err
	}
	for _, key := range r.keys {
		if !r.used[key] {
			r.err = fmt.Errorf("asapv1: unknown metadata key %q", key)
			break
		}
	}
	return r.err
}
