package asapmsgpack

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// decoder is the inverse of encoder — only supports the subset of
// MessagePack used by the Marshal* functions in this package. Used by
// Unmarshal* round-trip helpers and tests.
type decoder struct {
	buf []byte
	pos int
}

func newDecoder(buf []byte) *decoder { return &decoder{buf: buf} }

func (d *decoder) readByte() (byte, error) {
	if d.pos >= len(d.buf) {
		return 0, errors.New("asapmsgpack: unexpected end of buffer")
	}
	b := d.buf[d.pos]
	d.pos++
	return b, nil
}

func (d *decoder) readN(n int) ([]byte, error) {
	if d.pos+n > len(d.buf) {
		return nil, fmt.Errorf(
			"asapmsgpack: need %d bytes, have %d", n, len(d.buf)-d.pos)
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}

// readArrayLen reads a fixarray / array16 / array32 header and returns
// the element count.
func (d *decoder) readArrayLen() (int, error) {
	b, err := d.readByte()
	if err != nil {
		return 0, err
	}
	switch {
	case b&0xf0 == 0x90:
		return int(b & 0x0f), nil
	case b == 0xdc:
		nb, err := d.readN(2)
		if err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint16(nb)), nil
	case b == 0xdd:
		nb, err := d.readN(4)
		if err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint32(nb)), nil
	}
	return 0, fmt.Errorf("asapmsgpack: expected array header, got 0x%02x", b)
}

// readUint reads a msgpack integer as uint64. Accepts positive fixint,
// uint8, uint16, uint32, uint64. Returns an error on negative ints.
func (d *decoder) readUint() (uint64, error) {
	b, err := d.readByte()
	if err != nil {
		return 0, err
	}
	if b <= 0x7f {
		return uint64(b), nil
	}
	switch b {
	case 0xcc:
		v, err := d.readByte()
		return uint64(v), err
	case 0xcd:
		nb, err := d.readN(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(nb)), nil
	case 0xce:
		nb, err := d.readN(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint32(nb)), nil
	case 0xcf:
		nb, err := d.readN(8)
		if err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(nb), nil
	}
	return 0, fmt.Errorf("asapmsgpack: expected uint, got 0x%02x", b)
}

// readInt reads any signed msgpack integer as int64. Also accepts
// positive forms because rmp_serde may emit them for non-negative i* values.
func (d *decoder) readInt() (int64, error) {
	// Peek.
	if d.pos >= len(d.buf) {
		return 0, errors.New("asapmsgpack: unexpected end of buffer")
	}
	b := d.buf[d.pos]
	// Negative fixint: 0xe0..0xff.
	if b >= 0xe0 {
		d.pos++
		return int64(int8(b)), nil
	}
	// Non-negative forms share decoding with unsigned.
	if b <= 0x7f || (b >= 0xcc && b <= 0xcf) {
		v, err := d.readUint()
		if err != nil {
			return 0, err
		}
		return int64(v), nil
	}
	d.pos++
	switch b {
	case 0xd0:
		v, err := d.readByte()
		return int64(int8(v)), err
	case 0xd1:
		nb, err := d.readN(2)
		if err != nil {
			return 0, err
		}
		return int64(int16(binary.BigEndian.Uint16(nb))), nil
	case 0xd2:
		nb, err := d.readN(4)
		if err != nil {
			return 0, err
		}
		return int64(int32(binary.BigEndian.Uint32(nb))), nil
	case 0xd3:
		nb, err := d.readN(8)
		if err != nil {
			return 0, err
		}
		return int64(binary.BigEndian.Uint64(nb)), nil
	}
	return 0, fmt.Errorf("asapmsgpack: expected int, got 0x%02x", b)
}

// readBool reads a msgpack bool (0xc3 = true, 0xc2 = false).
func (d *decoder) readBool() (bool, error) {
	b, err := d.readByte()
	if err != nil {
		return false, err
	}
	switch b {
	case 0xc3:
		return true, nil
	case 0xc2:
		return false, nil
	}
	return false, fmt.Errorf("asapmsgpack: expected bool (0xc2/0xc3), got 0x%02x", b)
}

func (d *decoder) readFloat64() (float64, error) {
	b, err := d.readByte()
	if err != nil {
		return 0, err
	}
	if b != 0xcb {
		return 0, fmt.Errorf(
			"asapmsgpack: expected float64 (0xcb), got 0x%02x", b)
	}
	nb, err := d.readN(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(nb)), nil
}

func (d *decoder) readString() (string, error) {
	b, err := d.readByte()
	if err != nil {
		return "", err
	}
	var n int
	switch {
	case b&0xe0 == 0xa0:
		n = int(b & 0x1f)
	case b == 0xd9:
		v, err := d.readByte()
		if err != nil {
			return "", err
		}
		n = int(v)
	case b == 0xda:
		nb, err := d.readN(2)
		if err != nil {
			return "", err
		}
		n = int(binary.BigEndian.Uint16(nb))
	case b == 0xdb:
		nb, err := d.readN(4)
		if err != nil {
			return "", err
		}
		n = int(binary.BigEndian.Uint32(nb))
	default:
		return "", fmt.Errorf(
			"asapmsgpack: expected string header, got 0x%02x", b)
	}
	sb, err := d.readN(n)
	if err != nil {
		return "", err
	}
	return string(sb), nil
}

// readU8Array reads an array-of-u8 serialized in the rmp_serde
// `Vec<u8>` shape (fixarray + positive-fixint elements).
func (d *decoder) readU8Array() ([]byte, error) {
	n, err := d.readArrayLen()
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := d.readUint()
		if err != nil {
			return nil, err
		}
		if v > 0xff {
			return nil, fmt.Errorf(
				"asapmsgpack: element %d = %d out of u8 range", i, v)
		}
		out[i] = byte(v)
	}
	return out, nil
}

func (d *decoder) readFloat64Matrix() ([][]float64, error) {
	rows, err := d.readArrayLen()
	if err != nil {
		return nil, err
	}
	out := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		cols, err := d.readArrayLen()
		if err != nil {
			return nil, err
		}
		row := make([]float64, cols)
		for c := 0; c < cols; c++ {
			v, err := d.readFloat64()
			if err != nil {
				return nil, err
			}
			row[c] = v
		}
		out[r] = row
	}
	return out, nil
}

func (d *decoder) done() error {
	if d.pos != len(d.buf) {
		return fmt.Errorf(
			"asapmsgpack: trailing bytes after payload (read %d of %d)",
			d.pos, len(d.buf))
	}
	return nil
}
