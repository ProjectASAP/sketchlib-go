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
// Marshal builds the metadata with a MetadataWriter (metadata_version first,
// then HashSpec for a sketch that hashes, then the structural params in the
// kind's canonical order), the payload with an Encoder, and frames both with
// Encode:
//
//	md := asapv1.NewMetadataWriter()
//	md.HashSpec(asapv1.StandardProfile(), asapv1.MatrixSeedIndex)
//	md.Uint("rows", uint64(s.rows))
//	p := asapv1.NewEncoder()
//	p.Array(1)
//	asapv1.EncodeInts(p, s.counts)
//	return asapv1.Encode(asapv1.KindCountMin, md.Bytes(), p.Bytes())
//
// Unmarshal opens the envelope with Open (Split for a type that owns more than
// one kind_id), validates the metadata with a MetadataReader, reads the
// payload with a Decoder, and checks every structural invariant before
// building state:
//
//	md, p, err := asapv1.Open(b, asapv1.KindCountMin)
//	if err != nil {
//		return err
//	}
//	md.HashSpec(asapv1.StandardProfile(), asapv1.MatrixSeedIndex)
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
// MetadataReader and Decoder keep their first error, so a run of reads needs
// one check at Finish. MetadataReader.Finish also rejects any key no accessor
// consumed, and Decoder.Finish rejects trailing bytes.
//
// Encoder writes every integer in the uint family when it is non-negative and
// in the int family otherwise, at the minimal width, and every float as
// float64 unless the spec names float32. Fields the spec types as bin use
// Encoder.Bin; every other byte sequence is an array.
//
// Golden tests use package asapv1test: for each fixture of the kind, the
// fixture unmarshals to the known state and re-marshals to the same bytes
// (CheckRoundTrip), and the known state marshals to the fixture
// (CheckMarshal).
package asapv1
