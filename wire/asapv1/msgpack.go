package asapv1

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Unsigned is the set of unsigned integer types the slice helpers accept.
type Unsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Signed is the set of signed integer types the slice helpers accept.
type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// Encoder writes the MessagePack subset ASAPv1 uses, choosing every family and
// width exactly as rmp_serde does.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{buf: make([]byte, 0, 64)} }

// Bytes returns the encoded bytes.
func (e *Encoder) Bytes() []byte { return e.buf }

// Array writes an array header for n elements.
func (e *Encoder) Array(n int) { e.header(n, 0x90, 15, 0xdc, 0xdd) }

// Map writes a map header for n key/value pairs.
func (e *Encoder) Map(n int) { e.header(n, 0x80, 15, 0xde, 0xdf) }

func (e *Encoder) header(n int, fix byte, fixMax int, m16, m32 byte) {
	switch {
	case n <= fixMax:
		e.buf = append(e.buf, fix|byte(n))
	case n <= math.MaxUint16:
		e.buf = append(e.buf, m16)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(n))
	default:
		e.buf = append(e.buf, m32)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	}
}

// Uint writes v in the uint family at its minimal width.
func (e *Encoder) Uint(v uint64) {
	switch {
	case v <= 0x7f:
		e.buf = append(e.buf, byte(v))
	case v <= math.MaxUint8:
		e.buf = append(e.buf, 0xcc, byte(v))
	case v <= math.MaxUint16:
		e.buf = append(e.buf, 0xcd)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(v))
	case v <= math.MaxUint32:
		e.buf = append(e.buf, 0xce)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(v))
	default:
		e.buf = append(e.buf, 0xcf)
		e.buf = binary.BigEndian.AppendUint64(e.buf, v)
	}
}

// Int writes v at its minimal width: the uint family when v >= 0, the int
// family otherwise.
func (e *Encoder) Int(v int64) {
	switch {
	case v >= 0:
		e.Uint(uint64(v))
	case v >= -32:
		e.buf = append(e.buf, byte(v))
	case v >= math.MinInt8:
		e.buf = append(e.buf, 0xd0, byte(v))
	case v >= math.MinInt16:
		e.buf = append(e.buf, 0xd1)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(v))
	case v >= math.MinInt32:
		e.buf = append(e.buf, 0xd2)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(v))
	default:
		e.buf = append(e.buf, 0xd3)
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(v))
	}
}

// Float64 writes v as float64 (0xcb).
func (e *Encoder) Float64(v float64) {
	e.buf = append(e.buf, 0xcb)
	e.buf = binary.BigEndian.AppendUint64(e.buf, math.Float64bits(v))
}

// Float32 writes v as float32 (0xca).
func (e *Encoder) Float32(v float32) {
	e.buf = append(e.buf, 0xca)
	e.buf = binary.BigEndian.AppendUint32(e.buf, math.Float32bits(v))
}

// Bool writes true (0xc3) or false (0xc2).
func (e *Encoder) Bool(v bool) {
	if v {
		e.buf = append(e.buf, 0xc3)
	} else {
		e.buf = append(e.buf, 0xc2)
	}
}

// Nil writes nil (0xc0).
func (e *Encoder) Nil() { e.buf = append(e.buf, 0xc0) }

// Str writes s as a str at its minimal width.
func (e *Encoder) Str(s string) {
	n := len(s)
	switch {
	case n <= 31:
		e.buf = append(e.buf, 0xa0|byte(n))
	case n <= math.MaxUint8:
		e.buf = append(e.buf, 0xd9, byte(n))
	case n <= math.MaxUint16:
		e.buf = append(e.buf, 0xda)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(n))
	default:
		e.buf = append(e.buf, 0xdb)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	}
	e.buf = append(e.buf, s...)
}

// Bin writes b as a bin at its minimal width.
func (e *Encoder) Bin(b []byte) {
	n := len(b)
	switch {
	case n <= math.MaxUint8:
		e.buf = append(e.buf, 0xc4, byte(n))
	case n <= math.MaxUint16:
		e.buf = append(e.buf, 0xc5)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(n))
	default:
		e.buf = append(e.buf, 0xc6)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	}
	e.buf = append(e.buf, b...)
}

// EncodeUints writes xs as an array of uint-family integers.
func EncodeUints[T Unsigned](e *Encoder, xs []T) {
	e.Array(len(xs))
	for _, x := range xs {
		e.Uint(uint64(x))
	}
}

// EncodeInts writes xs as an array of integers under the family/width rule.
func EncodeInts[T Signed](e *Encoder, xs []T) {
	e.Array(len(xs))
	for _, x := range xs {
		e.Int(int64(x))
	}
}

// EncodeFloat64s writes xs as an array of float64.
func EncodeFloat64s(e *Encoder, xs []float64) {
	e.Array(len(xs))
	for _, x := range xs {
		e.Float64(x)
	}
}

// EncodeStrs writes xs as an array of str.
func EncodeStrs(e *Encoder, xs []string) {
	e.Array(len(xs))
	for _, x := range xs {
		e.Str(x)
	}
}

// Decoder reads the MessagePack subset ASAPv1 uses. It keeps the first error:
// after a failure every read returns a zero value, and Err or Finish reports
// the error.
type Decoder struct {
	buf []byte
	pos int
	err error
}

// NewDecoder returns a Decoder over b.
func NewDecoder(b []byte) *Decoder { return &Decoder{buf: b} }

// Err returns the first error the Decoder hit, or nil.
func (d *Decoder) Err() error { return d.err }

// Finish returns the first error, or an error if unread bytes remain.
func (d *Decoder) Finish() error {
	if d.err == nil && d.pos != len(d.buf) {
		d.err = fmt.Errorf("asapv1: %d trailing bytes", len(d.buf)-d.pos)
	}
	return d.err
}

// Fail records err as the Decoder's error unless one is already recorded.
func (d *Decoder) Fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *Decoder) failf(format string, args ...any) {
	d.Fail(fmt.Errorf("asapv1: "+format, args...))
}

func (d *Decoder) next() (byte, bool) {
	if d.err != nil {
		return 0, false
	}
	if d.pos >= len(d.buf) {
		d.failf("unexpected end of input at offset %d", d.pos)
		return 0, false
	}
	b := d.buf[d.pos]
	d.pos++
	return b, true
}

func (d *Decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.buf)-d.pos {
		d.failf("need %d bytes at offset %d, have %d", n, d.pos, len(d.buf)-d.pos)
		return nil
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b
}

func (d *Decoder) peek() (byte, bool) {
	if d.err != nil {
		return 0, false
	}
	if d.pos >= len(d.buf) {
		d.failf("unexpected end of input at offset %d", d.pos)
		return 0, false
	}
	return d.buf[d.pos], true
}

func (d *Decoder) be(n int) uint64 {
	b := d.take(n)
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// count reads an array or map header and checks that n entries, each at least
// perEntry bytes, fit in the remaining input.
func (d *Decoder) count(fix byte, m16, m32 byte, perEntry int, what string) int {
	b, ok := d.next()
	if !ok {
		return 0
	}
	var n uint64
	switch {
	case b&0xf0 == fix:
		n = uint64(b & 0x0f)
	case b == m16:
		n = d.be(2)
	case b == m32:
		n = d.be(4)
	default:
		d.failf("expected %s header, got 0x%02x at offset %d", what, b, d.pos-1)
		return 0
	}
	if d.err == nil && n*uint64(perEntry) > uint64(len(d.buf)-d.pos) {
		d.failf("%s of %d entries exceeds the %d remaining bytes", what, n, len(d.buf)-d.pos)
		return 0
	}
	return int(n)
}

// Array reads an array header and returns its element count.
func (d *Decoder) Array() int { return d.count(0x90, 0xdc, 0xdd, 1, "array") }

// Map reads a map header and returns its key/value pair count.
func (d *Decoder) Map() int { return d.count(0x80, 0xde, 0xdf, 2, "map") }

// ExpectArray reads an array header and fails unless it holds n elements.
func (d *Decoder) ExpectArray(n int) {
	if got := d.Array(); d.err == nil && got != n {
		d.failf("expected array of %d elements, got %d", n, got)
	}
}

// integer reads any msgpack integer as a sign flag and magnitude bits.
func (d *Decoder) integer() (v uint64, negative bool) {
	b, ok := d.next()
	if !ok {
		return 0, false
	}
	switch {
	case b <= 0x7f:
		return uint64(b), false
	case b >= 0xe0:
		return uint64(int64(int8(b))), true
	}
	switch b {
	case 0xcc:
		return d.be(1), false
	case 0xcd:
		return d.be(2), false
	case 0xce:
		return d.be(4), false
	case 0xcf:
		return d.be(8), false
	case 0xd0:
		v := int64(int8(d.be(1)))
		return uint64(v), v < 0
	case 0xd1:
		v := int64(int16(d.be(2)))
		return uint64(v), v < 0
	case 0xd2:
		v := int64(int32(d.be(4)))
		return uint64(v), v < 0
	case 0xd3:
		v := int64(d.be(8))
		return uint64(v), v < 0
	}
	d.failf("expected integer, got 0x%02x at offset %d", b, d.pos-1)
	return 0, false
}

// Uint reads a non-negative integer of either family.
func (d *Decoder) Uint() uint64 {
	v, negative := d.integer()
	if negative {
		d.failf("expected non-negative integer, got %d", int64(v))
		return 0
	}
	return v
}

// Int reads an integer of either family that fits int64.
func (d *Decoder) Int() int64 {
	v, negative := d.integer()
	if !negative && v > math.MaxInt64 {
		d.failf("integer %d overflows int64", v)
		return 0
	}
	return int64(v)
}

// Uint8 reads a non-negative integer that fits uint8.
func (d *Decoder) Uint8() uint8 { return DecodeUint[uint8](d) }

// Uint32 reads a non-negative integer that fits uint32.
func (d *Decoder) Uint32() uint32 { return DecodeUint[uint32](d) }

// Int32 reads an integer that fits int32.
func (d *Decoder) Int32() int32 { return DecodeInt[int32](d) }

// DecodeUint reads a non-negative integer and fails unless it fits T.
func DecodeUint[T Unsigned](d *Decoder) T {
	v := d.Uint()
	if uint64(T(v)) != v {
		d.failf("integer %d overflows %T", v, T(0))
		return 0
	}
	return T(v)
}

// DecodeInt reads an integer and fails unless it fits T.
func DecodeInt[T Signed](d *Decoder) T {
	v := d.Int()
	if int64(T(v)) != v {
		d.failf("integer %d overflows %T", v, T(0))
		return 0
	}
	return T(v)
}

// Float64 reads a float64 (0xcb).
func (d *Decoder) Float64() float64 {
	if b, ok := d.next(); ok && b != 0xcb {
		d.failf("expected float64, got 0x%02x at offset %d", b, d.pos-1)
	}
	return math.Float64frombits(d.be(8))
}

// Float32 reads a float32 (0xca).
func (d *Decoder) Float32() float32 {
	if b, ok := d.next(); ok && b != 0xca {
		d.failf("expected float32, got 0x%02x at offset %d", b, d.pos-1)
	}
	return math.Float32frombits(uint32(d.be(4)))
}

// Bool reads a bool.
func (d *Decoder) Bool() bool {
	b, ok := d.next()
	if ok && b != 0xc2 && b != 0xc3 {
		d.failf("expected bool, got 0x%02x at offset %d", b, d.pos-1)
	}
	return b == 0xc3
}

// Nil consumes a nil and reports true if the next value is nil; otherwise it
// consumes nothing and reports false.
func (d *Decoder) Nil() bool {
	if b, ok := d.peek(); ok && b == 0xc0 {
		d.pos++
		return true
	}
	return false
}

// Str reads a str.
func (d *Decoder) Str() string {
	b, ok := d.next()
	if !ok {
		return ""
	}
	var n uint64
	switch {
	case b&0xe0 == 0xa0:
		n = uint64(b & 0x1f)
	case b == 0xd9:
		n = d.be(1)
	case b == 0xda:
		n = d.be(2)
	case b == 0xdb:
		n = d.be(4)
	default:
		d.failf("expected str, got 0x%02x at offset %d", b, d.pos-1)
		return ""
	}
	return string(d.take(int(n)))
}

// Bin reads a bin and returns a copy of its bytes.
func (d *Decoder) Bin() []byte {
	b, ok := d.next()
	if !ok {
		return nil
	}
	var n uint64
	switch b {
	case 0xc4:
		n = d.be(1)
	case 0xc5:
		n = d.be(2)
	case 0xc6:
		n = d.be(4)
	default:
		d.failf("expected bin, got 0x%02x at offset %d", b, d.pos-1)
		return nil
	}
	raw := d.take(int(n))
	if raw == nil {
		return nil
	}
	return append(make([]byte, 0, len(raw)), raw...)
}

// Skip reads past one complete value of any type.
func (d *Decoder) Skip() {
	for pending := 1; pending > 0 && d.err == nil; pending-- {
		b, ok := d.next()
		if !ok {
			return
		}
		switch {
		case b <= 0x7f || b >= 0xe0 || b == 0xc0 || b == 0xc2 || b == 0xc3:
		case b&0xf0 == 0x80:
			pending += 2 * int(b&0x0f)
		case b&0xf0 == 0x90:
			pending += int(b & 0x0f)
		case b&0xe0 == 0xa0:
			d.take(int(b & 0x1f))
		case b == 0xcc || b == 0xd0:
			d.take(1)
		case b == 0xcd || b == 0xd1:
			d.take(2)
		case b == 0xce || b == 0xd2 || b == 0xca:
			d.take(4)
		case b == 0xcf || b == 0xd3 || b == 0xcb:
			d.take(8)
		case b == 0xc4 || b == 0xd9:
			d.take(int(d.be(1)))
		case b == 0xc5 || b == 0xda:
			d.take(int(d.be(2)))
		case b == 0xc6 || b == 0xdb:
			d.take(int(d.be(4)))
		case b == 0xdc:
			pending += int(d.be(2))
		case b == 0xdd:
			pending += int(d.be(4))
		case b == 0xde:
			pending += 2 * int(d.be(2))
		case b == 0xdf:
			pending += 2 * int(d.be(4))
		default:
			d.failf("unsupported msgpack type 0x%02x at offset %d", b, d.pos-1)
		}
	}
}

// DecodeUints reads an array of non-negative integers, each of which must fit T.
func DecodeUints[T Unsigned](d *Decoder) []T {
	n := d.Array()
	if d.err != nil {
		return nil
	}
	out := make([]T, n)
	for i := range out {
		out[i] = DecodeUint[T](d)
	}
	return out
}

// DecodeInts reads an array of integers, each of which must fit T.
func DecodeInts[T Signed](d *Decoder) []T {
	n := d.Array()
	if d.err != nil {
		return nil
	}
	out := make([]T, n)
	for i := range out {
		out[i] = DecodeInt[T](d)
	}
	return out
}

// DecodeFloat64s reads an array of float64.
func DecodeFloat64s(d *Decoder) []float64 {
	n := d.Array()
	if d.err != nil {
		return nil
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = d.Float64()
	}
	return out
}

// DecodeStrs reads an array of str.
func DecodeStrs(d *Decoder) []string {
	n := d.Array()
	if d.err != nil {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = d.Str()
	}
	return out
}
