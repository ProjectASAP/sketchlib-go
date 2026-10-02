package asapv1

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func TestStandardProfile(t *testing.T) {
	p := StandardProfile()
	if p.ID != "projectasap.xxh3.seedlist.v1" || p.Algorithm != "xxh3_64_128" ||
		p.SeedDerivation != "seed_list_index_wrap" || p.InputEncoding != "projectasap.input.v1" ||
		p.CanonicalSeedIndex != 5 || p.MatrixSeedIndex != 0 {
		t.Fatalf("StandardProfile = %+v", p)
	}
	if len(p.SeedList) != 20 || p.SeedList[0] != 0xcafe3553 || p.SeedList[1] != 0xade3415118 || p.SeedList[19] != 0xdb0c2e0d {
		t.Fatalf("seed list = %#x", p.SeedList)
	}
	p.SeedList[0] = 0
	if StandardProfile().SeedList[0] != 0xcafe3553 || common.SeedList()[0] != 0xcafe3553 {
		t.Fatal("StandardProfile shares its seed list")
	}
}

func TestMetadataWriterLayout(t *testing.T) {
	w := NewMetadataWriter(1)
	w.Uint("k", 200)
	w.Str("t", "f64")
	got := w.Bytes()
	want := append(mustHex(t, "83 b0"), "metadata_version"...)
	want = append(want, mustHex(t, "01 a16b ccc8 a174 a3663634")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Bytes = %x, want %x", got, want)
	}
	if got := NewMetadataWriter(2).Bytes(); got[len(got)-1] != 0x02 {
		t.Fatalf("NewMetadataWriter(2) = %x", got)
	}
}

func TestHashSpecOrder(t *testing.T) {
	w := NewMetadataWriter(1)
	w.HashSpec(StandardProfile(), SeedIndexMatrix)
	w.Uint("rows", 2)
	r, err := ReadMetadata(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"metadata_version", "hash_profile_id", "hash_algorithm", "seed_derivation",
		"input_encoding", "seed_list", "matrix_seed_index", "rows"}
	if strings.Join(r.keys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("keys = %v, want %v", r.keys, wantKeys)
	}
	for idx, want := range map[SeedIndex]string{SeedIndexNone: "seed_list", SeedIndexCanonical: "canonical_seed_index", SeedIndexMatrix: "matrix_seed_index"} {
		w := NewMetadataWriter(1)
		w.HashSpec(StandardProfile(), idx)
		r, err := ReadMetadata(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if last := r.keys[len(r.keys)-1]; last != want {
			t.Errorf("seed index %d: keys = %v, want last %q", idx, r.keys, want)
		}
	}
}

func hashSpecMetadata(p HashProfile, idx SeedIndex, extra func(w *MetadataWriter)) []byte {
	w := NewMetadataWriter(1)
	w.HashSpec(p, idx)
	if extra != nil {
		extra(w)
	}
	return w.Bytes()
}

func TestMetadataReaderRoundTrip(t *testing.T) {
	b := hashSpecMetadata(StandardProfile(), SeedIndexCanonical, func(w *MetadataWriter) {
		w.Uint("precision", 12)
		w.Int("offset", -3)
		w.Float64("alpha", 0.01)
		w.Bool("flag", true)
		w.Str("mode", "fast")
		w.Strs("schema", []string{"a", "b"})
		w.Uint("seed", 1<<40)
		EncodeInts(w.Field("custom"), []int32{-1, 2})
	})
	r, err := ReadMetadata(b)
	if err != nil {
		t.Fatal(err)
	}
	var custom []int32
	r.Field("custom", func(d *Decoder) { custom = DecodeInts[int32](d) })
	schema := r.Strs("schema")
	mode := r.Str("mode")
	flag := r.Bool("flag")
	alpha := r.Float64("alpha")
	offset := r.Int64("offset")
	precision := r.Uint8("precision")
	if !r.Has("seed") || r.Has("absent") {
		t.Fatal("Has")
	}
	seed := r.Uint64("seed")
	r.HashSpec(StandardProfile(), SeedIndexCanonical)
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}
	if precision != 12 || offset != -3 || alpha != 0.01 || !flag || mode != "fast" ||
		len(schema) != 2 || schema[1] != "b" || seed != 1<<40 || len(custom) != 2 || custom[0] != -1 {
		t.Fatalf("values = %d %d %v %v %q %q %d %v", precision, offset, alpha, flag, mode, schema, seed, custom)
	}
}

func TestMetadataReaderRejects(t *testing.T) {
	std := StandardProfile()
	withProfile := func(edit func(p *HashProfile)) []byte {
		p := StandardProfile()
		edit(&p)
		return hashSpecMetadata(p, SeedIndexMatrix, nil)
	}
	cases := map[string]struct {
		b    []byte
		read func(r *MetadataReader)
	}{
		"other profile id":       {withProfile(func(p *HashProfile) { p.ID = "custom.v1" }), nil},
		"other algorithm":        {withProfile(func(p *HashProfile) { p.Algorithm = "murmur" }), nil},
		"other seed derivation":  {withProfile(func(p *HashProfile) { p.SeedDerivation = "x" }), nil},
		"other input encoding":   {withProfile(func(p *HashProfile) { p.InputEncoding = "x" }), nil},
		"other seed":             {withProfile(func(p *HashProfile) { p.SeedList[3]++ }), nil},
		"short seed list":        {withProfile(func(p *HashProfile) { p.SeedList = p.SeedList[:19] }), nil},
		"other matrix index":     {withProfile(func(p *HashProfile) { p.MatrixSeedIndex = 1 }), nil},
		"wrong seed-index key":   {hashSpecMetadata(std, SeedIndexCanonical, nil), nil},
		"missing seed-index key": {hashSpecMetadata(std, SeedIndexNone, nil), nil},
		"unknown key": {hashSpecMetadata(std, SeedIndexMatrix, func(w *MetadataWriter) {
			w.Uint("extra", 1)
		}), nil},
		"missing key": {hashSpecMetadata(std, SeedIndexMatrix, nil), func(r *MetadataReader) {
			r.Uint32("rows")
		}},
		"wrong type": {hashSpecMetadata(std, SeedIndexMatrix, func(w *MetadataWriter) {
			w.Str("rows", "2")
		}), func(r *MetadataReader) { r.Uint32("rows") }},
		"out of range": {hashSpecMetadata(std, SeedIndexMatrix, func(w *MetadataWriter) {
			w.Uint("precision", 256)
		}), func(r *MetadataReader) { r.Uint8("precision") }},
		"unexpected value": {hashSpecMetadata(std, SeedIndexMatrix, func(w *MetadataWriter) {
			w.Str("counter_type", "i32")
		}), func(r *MetadataReader) { r.ExpectStr("counter_type", "i64") }},
		"value not fully read": {hashSpecMetadata(std, SeedIndexMatrix, func(w *MetadataWriter) {
			EncodeUints(w.Field("arr"), []uint8{1, 2})
		}), func(r *MetadataReader) { r.Field("arr", func(d *Decoder) { d.Array() }) }},
	}
	for name, c := range cases {
		r, err := ReadMetadata(c.b)
		if err != nil {
			t.Errorf("%s: ReadMetadata: %v", name, err)
			continue
		}
		if c.read != nil {
			c.read(r)
		}
		r.HashSpec(std, SeedIndexMatrix)
		if r.Finish() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadMetadataRejects(t *testing.T) {
	versionAt := func(v uint64) []byte {
		e := NewEncoder()
		e.Map(1)
		e.Str("metadata_version")
		e.Uint(v)
		return e.Bytes()
	}
	dup := NewMetadataWriter(1)
	dup.Uint("k", 1)
	dup.Uint("k", 2)
	noVersion := NewEncoder()
	noVersion.Map(1)
	noVersion.Str("k")
	noVersion.Uint(1)
	intKey := NewEncoder()
	intKey.Map(1)
	intKey.Uint(1)
	intKey.Uint(1)
	cases := map[string][]byte{
		"empty":                nil,
		"array":                {0x90},
		"metadata_version 256": versionAt(256),
		"no version":           noVersion.Bytes(),
		"duplicate key":        dup.Bytes(),
		"integer key":          intKey.Bytes(),
		"truncated":            versionAt(1)[:5],
		"trailing bytes":       append(versionAt(1), 0x00),
	}
	for name, b := range cases {
		if _, err := ReadMetadata(b); err == nil {
			t.Errorf("%s: ReadMetadata accepted %x", name, b)
		}
	}
	for _, v := range []uint64{0, 1, 2, 255} {
		r, err := ReadMetadata(versionAt(v))
		if err != nil {
			t.Errorf("ReadMetadata rejected metadata_version %d: %v", v, err)
			continue
		}
		if r.Version() != uint8(v) || r.Finish() != nil {
			t.Errorf("Version() = %d (%v), want %d", r.Version(), r.Err(), v)
		}
	}
}

func TestExpectVersion(t *testing.T) {
	for _, c := range []struct {
		version  uint8
		accepted []uint8
		ok       bool
	}{
		{1, []uint8{1}, true},
		{2, []uint8{1, 2}, true},
		{2, []uint8{1}, false},
		{1, []uint8{2}, false},
		{0, nil, false},
	} {
		r, err := ReadMetadata(NewMetadataWriter(c.version).Bytes())
		if err != nil {
			t.Fatal(err)
		}
		r.ExpectVersion(c.accepted...)
		if ok := r.Finish() == nil; ok != c.ok {
			t.Errorf("version %d, accepted %v: ok = %v, want %v", c.version, c.accepted, ok, c.ok)
		}
	}
}

func TestUnknownSeedIndex(t *testing.T) {
	w := NewMetadataWriter(1)
	w.HashSpec(StandardProfile(), SeedIndex(7))
	if w.Err() == nil {
		t.Error("MetadataWriter accepted SeedIndex(7)")
	}
	if _, err := Marshal(KindHLLClassic, w, NewEncoder()); err == nil {
		t.Error("Marshal ignored the metadata error")
	}
	r, err := ReadMetadata(hashSpecMetadata(StandardProfile(), SeedIndexNone, nil))
	if err != nil {
		t.Fatal(err)
	}
	r.HashSpec(StandardProfile(), SeedIndex(-1))
	if r.Finish() == nil {
		t.Error("MetadataReader accepted SeedIndex(-1)")
	}
}

func TestMetadataWriterRejectsInvalidUTF8(t *testing.T) {
	w := NewMetadataWriter(1)
	w.Str("key_type", "\xff")
	if w.Err() == nil {
		t.Fatal("MetadataWriter accepted an invalid UTF-8 value")
	}
	w = NewMetadataWriter(1)
	w.Uint("\xc3", 1)
	if w.Err() == nil {
		t.Fatal("MetadataWriter accepted an invalid UTF-8 key")
	}
}

func TestMetadataReaderKeepsFirstError(t *testing.T) {
	r, err := ReadMetadata(NewMetadataWriter(1).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r.Uint32("missing")
	first := r.Err()
	if first == nil {
		t.Fatal("missing key not reported")
	}
	r.Str("also_missing")
	if r.Finish() != first {
		t.Fatalf("Finish = %v, want %v", r.Finish(), first)
	}
}
