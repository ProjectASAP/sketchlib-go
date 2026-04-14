// Package asapmsgpack implements the narrow, hand-rolled MessagePack wire
// format used on ASAPQuery-backend's sketch-core ingest path. It is the
// Go producer side of the cross-language contract defined by PR I in the
// ASAPQuery-backend repo (modified-OTLP `*_ENCODING_MSGPACK = 3` path).
//
// Why hand-rolled rather than vmihailenco/msgpack/v5:
//
//   - The Rust consumer uses `rmp_serde` with default (compact) settings,
//     which serializes Rust structs as fixed-length arrays in declaration
//     order and serializes `Vec<u8>` as an **array of positive fixints**,
//     not as MessagePack `bin`. Generic Go msgpack libraries encode
//     `[]byte` as `bin` by default, producing bytes that cannot be
//     deserialized on the Rust side.
//
//   - The contract only covers four sketch types today (CountMin,
//     CountSketch, DDSketch, HLL). Writing a 50-line encoder shim gives
//     byte-exact control and drops a dependency.
//
// Wire-format reference for each supported sketch type lives alongside
// the `Marshal*` function in its per-sketch file. Golden fixtures pinned
// to the Rust side's `serialize_msgpack` output live in `encoder_test.go`;
// if the Rust wire format ever shifts, the tests fail loudly.
//
// Out of scope:
//
//   - KLL: sketchlib-go's KLL and ASAPQuery-backend's sketch-core `KllSketch`
//     do not share a byte-level backend serialization. A KLL MessagePack
//     path would need a shared backend (tracked as a follow-up — use the
//     `_ENCODING_PROTO` path for KLL, which decodes via lossy statistical
//     reconstruction on the Rust side).
//
//   - Delta transmission (`*_ENCODING_MSGPACK_DELTA = 4`): deferred until
//     sketch-core grows an `apply_delta` API.
package asapmsgpack

import (
	"encoding/binary"
	"math"
)

// encoder is a tiny byte-exact MessagePack writer that emits the subset
// of the format rmp_serde uses in compact mode. It intentionally does
// not handle maps, ext types, or bin — sketch-core's wire formats don't
// use any of those.
type encoder struct {
	buf []byte
}

func newEncoder() *encoder { return &encoder{buf: make([]byte, 0, 64)} }

// writeArrayLen emits a fixarray / array16 / array32 header.
func (e *encoder) writeArrayLen(n int) {
	switch {
	case n <= 15:
		e.buf = append(e.buf, 0x90|byte(n))
	case n <= 0xffff:
		e.buf = append(e.buf, 0xdc, byte(n>>8), byte(n))
	default:
		e.buf = append(e.buf, 0xdd,
			byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}

// writeUint emits the smallest unsigned integer form (positive fixint /
// uint8 / uint16 / uint32 / uint64) that fits v. This matches rmp_serde's
// compact serialization for Rust `u*` / `usize`.
func (e *encoder) writeUint(v uint64) {
	switch {
	case v <= 0x7f:
		e.buf = append(e.buf, byte(v))
	case v <= 0xff:
		e.buf = append(e.buf, 0xcc, byte(v))
	case v <= 0xffff:
		e.buf = append(e.buf, 0xcd, byte(v>>8), byte(v))
	case v <= 0xffffffff:
		e.buf = append(e.buf, 0xce,
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		e.buf = append(e.buf, 0xcf)
		e.buf = append(e.buf, b[:]...)
	}
}

// writeInt emits the smallest signed integer form that fits v. Matches
// rmp_serde's compact serialization for Rust `i*`.
func (e *encoder) writeInt(v int64) {
	if v >= 0 {
		e.writeUint(uint64(v))
		return
	}
	switch {
	case v >= -32:
		// Negative fixint: the byte representation is already the
		// two's-complement low 8 bits (0xe0..0xff).
		e.buf = append(e.buf, byte(v))
	case v >= -128:
		e.buf = append(e.buf, 0xd0, byte(v))
	case v >= -32768:
		e.buf = append(e.buf, 0xd1, byte(v>>8), byte(v))
	case v >= -2147483648:
		e.buf = append(e.buf, 0xd2,
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(v))
		e.buf = append(e.buf, 0xd3)
		e.buf = append(e.buf, b[:]...)
	}
}

// writeFloat64 emits a msgpack float64 (0xcb + 8 big-endian IEEE-754
// bytes). Matches rmp_serde: no float32 compaction.
func (e *encoder) writeFloat64(v float64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	e.buf = append(e.buf, 0xcb)
	e.buf = append(e.buf, b[:]...)
}

// writeString emits the smallest string form (fixstr / str8 / str16 /
// str32). Matches rmp_serde's enum-as-string serialization for Rust
// unit variants (e.g. HllVariant::Regular → "Regular").
func (e *encoder) writeString(s string) {
	n := len(s)
	switch {
	case n <= 31:
		e.buf = append(e.buf, 0xa0|byte(n))
	case n <= 0xff:
		e.buf = append(e.buf, 0xd9, byte(n))
	case n <= 0xffff:
		e.buf = append(e.buf, 0xda, byte(n>>8), byte(n))
	default:
		e.buf = append(e.buf, 0xdb,
			byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	e.buf = append(e.buf, s...)
}

// writeU8Array emits an array-of-u8 by writing each element as a
// positive fixint. This matches rmp_serde's default `Vec<u8>`
// serialization — which is array-of-int, NOT msgpack `bin`. Generic
// Go msgpack libraries encode `[]byte` as bin and would be incompatible.
func (e *encoder) writeU8Array(bs []byte) {
	e.writeArrayLen(len(bs))
	for _, b := range bs {
		// b <= 0xff is always positive fixint- or uint8-shaped; since
		// the loop variable is uint8, writeUint picks the smallest form.
		e.writeUint(uint64(b))
	}
}

// writeFloat64Matrix emits a row-major `[][]float64` as a nested msgpack
// array-of-arrays. Row count and column count are derived from the slice
// — the caller is responsible for rectangular shape.
func (e *encoder) writeFloat64Matrix(m [][]float64) {
	e.writeArrayLen(len(m))
	for _, row := range m {
		e.writeArrayLen(len(row))
		for _, v := range row {
			e.writeFloat64(v)
		}
	}
}

// bytes returns the completed msgpack payload.
func (e *encoder) bytes() []byte { return e.buf }
