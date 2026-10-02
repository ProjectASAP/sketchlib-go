// Package asapv1 implements the shared parts of the ASAPv1 sketch wire format:
// the envelope, the kind_id registry, the MessagePack primitives and the
// metadata map. The byte contract is docs/asapv1_wire_format.md in
// asap_sketchlib; the golden byte-vectors in asapv1_golden/ are authoritative.
//
// A serialized sketch is
//
//	magic "ASAPv1" | version | kind_id_len | kind_id | metadata_len u32be | payload_len u32be | metadata | payload
//
// where metadata is a MessagePack map (Section 2 of the spec) and payload is a
// positional MessagePack array (Section 3).
//
// # Codec convention
//
// Every sketch type with an ASAPv1 encoding lives in its own package, which
// imports asapv1; asapv1 imports no sketch package. The type implements
// Marshaler and, through its pointer, Unmarshaler:
//
//	func (s *T) MarshalASAPv1() ([]byte, error)
//	func (s *T) UnmarshalASAPv1(b []byte) error
//
// UnmarshalASAPv1 replaces the receiver's whole state and leaves it unchanged
// on error. MarshalASAPv1 fails for a state its own UnmarshalASAPv1 would
// reject, so the encoder never emits bytes the decoder refuses.
//
// MarshalASAPv1 builds the metadata with a MetadataWriter (metadata_version
// first, then HashSpec for a sketch that hashes, then the structural params in
// the kind's canonical order) and the payload with an Encoder, then frames
// both with Marshal, which also returns any error either recorded:
//
//	md := asapv1.NewMetadataWriter(1)
//	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
//	md.Uint("rows", uint64(s.rows))
//	p := asapv1.NewEncoder()
//	p.Array(1)
//	asapv1.EncodeInts(p, s.counts)
//	return asapv1.Marshal(asapv1.KindCountMin, md, p)
//
// UnmarshalASAPv1 opens the envelope with Open (Split for a type that owns
// more than one kind_id), validates the metadata with a MetadataReader, reads
// the payload with a Decoder, and checks every structural invariant before
// building state. The codec checks the metadata versions it accepts:
//
//	md, p, err := asapv1.Open(b, asapv1.KindCountMin)
//	if err != nil {
//		return err
//	}
//	md.ExpectVersion(1)
//	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
//	rows := md.Uint32("rows")
//	if err := md.Finish(); err != nil {
//		return err
//	}
//	p.ExpectArray(1)
//	counts := asapv1.DecodeInts[int64](p)
//	if err := p.Finish(); err != nil {
//		return err
//	}
//
// Encoder, MetadataWriter, MetadataReader and Decoder keep their first error,
// so a run of calls needs one check at the end. MetadataReader.Finish also
// rejects any key no accessor consumed, and Decoder.Finish rejects trailing
// bytes.
//
// # Nested sketches
//
// A sketch whose whole payload is carried as one element of another sketch's
// payload (Hydra's KLL and UnivMon cells) also implements PayloadEncoder and,
// through its pointer, PayloadDecoder:
//
//	func (s *T) EncodeASAPv1Payload(e *asapv1.Encoder) error
//	func (s *T) DecodeASAPv1Payload(md *asapv1.MetadataReader, d *asapv1.Decoder) error
//
// EncodeASAPv1Payload writes the payload array alone. DecodeASAPv1Payload
// reads exactly one payload array from d, validates md as the sketch's own
// metadata and calls md.Finish, but not d.Finish. The parent builds md from
// the params its own metadata carries, with NewMetadataWriter and
// ReadMetadata. The sketch's MarshalASAPv1 and UnmarshalASAPv1 are written in
// terms of the two hooks.
//
// Sketches inlined field by field (the base matrix of CMSHeap and CSHeap,
// Elastic's light layer, UnivMon's layers) need no hook. EHSketchList carries
// a variant's kind_id, metadata and payload verbatim: split them from
// MarshalASAPv1 with Split, and rebuild with Encode before UnmarshalASAPv1.
// Encoder.Raw and Decoder.Raw move one already-encoded value.
//
// # Encoding rules
//
// Encoder writes every integer in the uint family when it is non-negative and
// in the int family otherwise, at the minimal width, and every float as
// float64 unless the spec names float32. Fields the spec types as bin use
// Encoder.Bin; every other byte sequence is an array. A str must be valid
// UTF-8: Encoder.Str records an error otherwise, and Decoder.Str rejects it.
//
// # Golden tests
//
// Golden tests use package asapv1test. For each fixture of the kind,
// CheckGolden marshals the known state and compares it to the fixture,
// unmarshals the fixture into a fresh value, compares that to the known state,
// and re-marshals it.
package asapv1
