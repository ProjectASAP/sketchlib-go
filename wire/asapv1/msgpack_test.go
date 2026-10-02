package asapv1

import (
	"bytes"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func encoded(write func(e *Encoder)) []byte {
	e := NewEncoder()
	write(e)
	return e.Bytes()
}

func TestEncoderUintWidths(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "00"},
		{1, "01"},
		{127, "7f"},
		{128, "cc 80"},
		{255, "cc ff"},
		{256, "cd 0100"},
		{300, "cd 012c"},
		{65535, "cd ffff"},
		{65536, "ce 00010000"},
		{math.MaxUint32, "ce ffffffff"},
		{math.MaxUint32 + 1, "cf 0000000100000000"},
		{math.MaxUint64, "cf ffffffffffffffff"},
	}
	for _, c := range cases {
		got := encoded(func(e *Encoder) { e.Uint(c.v) })
		if want := mustHex(t, c.want); !bytes.Equal(got, want) {
			t.Errorf("Uint(%d) = %x, want %x", c.v, got, want)
		}
		d := NewDecoder(got)
		if v := d.Uint(); v != c.v || d.Finish() != nil {
			t.Errorf("decode %x = %d (%v), want %d", got, v, d.Err(), c.v)
		}
	}
}

func TestEncoderIntWidths(t *testing.T) {
	cases := []struct {
		v    int64
		want string
	}{
		{0, "00"},
		{127, "7f"},
		{128, "cc 80"},
		{65536, "ce 00010000"},
		{math.MaxInt64, "cf 7fffffffffffffff"},
		{-1, "ff"},
		{-32, "e0"},
		{-33, "d0 df"},
		{-128, "d0 80"},
		{-129, "d1 ff7f"},
		{-32768, "d1 8000"},
		{-32769, "d2 ffff7fff"},
		{math.MinInt32, "d2 80000000"},
		{math.MinInt32 - 1, "d3 ffffffff7fffffff"},
		{math.MinInt64, "d3 8000000000000000"},
	}
	for _, c := range cases {
		got := encoded(func(e *Encoder) { e.Int(c.v) })
		if want := mustHex(t, c.want); !bytes.Equal(got, want) {
			t.Errorf("Int(%d) = %x, want %x", c.v, got, want)
		}
		d := NewDecoder(got)
		if v := d.Int(); v != c.v || d.Finish() != nil {
			t.Errorf("decode %x = %d (%v), want %d", got, v, d.Err(), c.v)
		}
	}
}

func TestEncoderScalars(t *testing.T) {
	cases := []struct {
		name  string
		write func(e *Encoder)
		want  string
	}{
		{"float64", func(e *Encoder) { e.Float64(1.5) }, "cb 3ff8000000000000"},
		{"float64 zero", func(e *Encoder) { e.Float64(0) }, "cb 0000000000000000"},
		{"float32", func(e *Encoder) { e.Float32(1.5) }, "ca 3fc00000"},
		{"true", func(e *Encoder) { e.Bool(true) }, "c3"},
		{"false", func(e *Encoder) { e.Bool(false) }, "c2"},
		{"nil", func(e *Encoder) { e.Nil() }, "c0"},
		{"empty str", func(e *Encoder) { e.Str("") }, "a0"},
		{"fixstr", func(e *Encoder) { e.Str("i64") }, "a3 693634"},
		{"fixstr 31", func(e *Encoder) { e.Str(strings.Repeat("a", 31)) }, "bf" + strings.Repeat("61", 31)},
		{"str8", func(e *Encoder) { e.Str(strings.Repeat("a", 32)) }, "d9 20" + strings.Repeat("61", 32)},
		{"empty bin", func(e *Encoder) { e.Bin(nil) }, "c4 00"},
		{"bin8", func(e *Encoder) { e.Bin([]byte{1, 2}) }, "c4 02 0102"},
		{"fixarray", func(e *Encoder) { e.Array(15) }, "9f"},
		{"array16", func(e *Encoder) { e.Array(16) }, "dc 0010"},
		{"array32", func(e *Encoder) { e.Array(65536) }, "dd 00010000"},
		{"fixmap", func(e *Encoder) { e.Map(15) }, "8f"},
		{"map16", func(e *Encoder) { e.Map(16) }, "de 0010"},
		{"map32", func(e *Encoder) { e.Map(65536) }, "df 00010000"},
		{"uints", func(e *Encoder) { EncodeUints(e, []uint32{0, 300}) }, "92 00 cd012c"},
		{"ints", func(e *Encoder) { EncodeInts(e, []int32{-1, 1}) }, "92 ff 01"},
		{"float64s", func(e *Encoder) { EncodeFloat64s(e, []float64{2}) }, "91 cb 4000000000000000"},
		{"strs", func(e *Encoder) { EncodeStrs(e, []string{"a"}) }, "91 a1 61"},
	}
	for _, c := range cases {
		if got, want := encoded(c.write), mustHex(t, c.want); !bytes.Equal(got, want) {
			t.Errorf("%s = %x, want %x", c.name, got, want)
		}
	}
}

func TestStrAndBinWidths(t *testing.T) {
	for _, n := range []int{0, 31, 32, 255, 256, 65535, 65536} {
		s := strings.Repeat("x", n)
		d := NewDecoder(encoded(func(e *Encoder) { e.Str(s); e.Bin([]byte(s)) }))
		if got := d.Str(); got != s {
			t.Errorf("Str round trip of %d bytes failed", n)
		}
		if got := d.Bin(); !bytes.Equal(got, []byte(s)) {
			t.Errorf("Bin round trip of %d bytes failed", n)
		}
		if err := d.Finish(); err != nil {
			t.Errorf("%d bytes: %v", n, err)
		}
	}
	got := encoded(func(e *Encoder) { e.Str(strings.Repeat("x", 256)) })
	if !bytes.Equal(got[:3], []byte{0xda, 0x01, 0x00}) {
		t.Errorf("str16 header = %x", got[:3])
	}
	got = encoded(func(e *Encoder) { e.Bin(make([]byte, 65536)) })
	if !bytes.Equal(got[:5], []byte{0xc6, 0x00, 0x01, 0x00, 0x00}) {
		t.Errorf("bin32 header = %x", got[:5])
	}
}

func TestDecoderAcceptsAnyIntegerForm(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"cd 0001", 1},
		{"cf 0000000000000005", 5},
		{"d0 05", 5},
		{"d3 0000000000000007", 7},
	}
	for _, c := range cases {
		d := NewDecoder(mustHex(t, c.in))
		if v := d.Uint(); v != c.want || d.Finish() != nil {
			t.Errorf("Uint(%s) = %d (%v), want %d", c.in, v, d.Err(), c.want)
		}
	}
}

func TestDecoderRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		read func(d *Decoder)
	}{
		{"negative as uint", "ff", func(d *Decoder) { d.Uint() }},
		{"uint64 past int64", "cf 8000000000000000", func(d *Decoder) { d.Int() }},
		{"uint8 overflow", "cd 0100", func(d *Decoder) { d.Uint8() }},
		{"uint32 overflow", "cf 0000000100000000", func(d *Decoder) { d.Uint32() }},
		{"int32 overflow", "d3 ffffffff7fffffff", func(d *Decoder) { d.Int32() }},
		{"float32 as float64", "ca 3fc00000", func(d *Decoder) { d.Float64() }},
		{"float64 as float32", "cb 3ff8000000000000", func(d *Decoder) { d.Float32() }},
		{"int as bool", "01", func(d *Decoder) { d.Bool() }},
		{"bin as str", "c4 00", func(d *Decoder) { d.Str() }},
		{"str as bin", "a0", func(d *Decoder) { d.Bin() }},
		{"map as array", "80", func(d *Decoder) { d.Array() }},
		{"array as map", "90", func(d *Decoder) { d.Map() }},
		{"str as int", "a0", func(d *Decoder) { d.Int() }},
		{"array length mismatch", "92 01 02", func(d *Decoder) { d.ExpectArray(3) }},
		{"array longer than input", "dd ffffffff", func(d *Decoder) { d.Array() }},
		{"map longer than input", "83 a1 61 01 a1 62 02", func(d *Decoder) { d.Map() }},
		{"truncated uint", "cd 01", func(d *Decoder) { d.Uint() }},
		{"truncated float", "cb 3ff8", func(d *Decoder) { d.Float64() }},
		{"truncated str", "a3 6162", func(d *Decoder) { d.Str() }},
		{"truncated bin", "c4 03 0102", func(d *Decoder) { d.Bin() }},
		{"empty input", "", func(d *Decoder) { d.Uint() }},
		{"element overflow", "92 01 cd0100", func(d *Decoder) { DecodeUints[uint8](d) }},
		{"signed element overflow", "91 d1 8000", func(d *Decoder) { DecodeInts[int8](d) }},
		{"trailing bytes", "01 02", func(d *Decoder) { d.Uint() }},
	}
	for _, c := range cases {
		d := NewDecoder(mustHex(t, c.in))
		c.read(d)
		if d.Finish() == nil {
			t.Errorf("%s: decoding %q succeeded", c.name, c.in)
		}
	}
}

func TestDecoderKeepsFirstError(t *testing.T) {
	d := NewDecoder(mustHex(t, "a0 01 02"))
	if v := d.Uint(); v != 0 || d.Err() == nil {
		t.Fatalf("Uint on str = %d, %v", v, d.Err())
	}
	first := d.Err()
	if v := d.Uint(); v != 0 {
		t.Errorf("read after error = %d, want 0", v)
	}
	if n := d.Array(); n != 0 {
		t.Errorf("Array after error = %d, want 0", n)
	}
	if d.Finish() != first {
		t.Errorf("Finish = %v, want first error %v", d.Finish(), first)
	}
}

func TestDecoderNil(t *testing.T) {
	d := NewDecoder(mustHex(t, "c0 a1 61"))
	if !d.Nil() {
		t.Fatal("Nil() = false on nil")
	}
	if d.Nil() {
		t.Fatal("Nil() = true on str")
	}
	if s := d.Str(); s != "a" {
		t.Fatalf("Str after Nil = %q", s)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDecoderSlices(t *testing.T) {
	d := NewDecoder(encoded(func(e *Encoder) {
		EncodeUints(e, []uint64{0, math.MaxUint64})
		EncodeInts(e, []int64{math.MinInt64, -1, 0, math.MaxInt64})
		EncodeFloat64s(e, []float64{0.5, -2})
		EncodeStrs(e, []string{"", "ab"})
		EncodeUints(e, []uint8{})
	}))
	u := DecodeUints[uint64](d)
	i := DecodeInts[int64](d)
	f := DecodeFloat64s(d)
	s := DecodeStrs(d)
	empty := DecodeUints[uint8](d)
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(u) != 2 || u[1] != math.MaxUint64 || len(i) != 4 || i[0] != math.MinInt64 || i[3] != math.MaxInt64 ||
		len(f) != 2 || f[1] != -2 || len(s) != 2 || s[1] != "ab" || len(empty) != 0 {
		t.Fatalf("slices = %v %v %v %q %v", u, i, f, s, empty)
	}
}

func TestBinCopiesInput(t *testing.T) {
	in := mustHex(t, "c4 02 0102")
	b := NewDecoder(in).Bin()
	in[2] = 9
	if b[0] != 1 {
		t.Fatal("Bin aliases its input")
	}
}

func TestSkip(t *testing.T) {
	value := encoded(func(e *Encoder) {
		e.Map(2)
		e.Str("a")
		e.Array(7)
		e.Uint(1 << 40)
		e.Int(-1 << 40)
		e.Float32(1)
		e.Float64(1)
		e.Bin(make([]byte, 300))
		e.Str(strings.Repeat("s", 40))
		e.Nil()
		e.Str("b")
		e.Map(1)
		e.Bool(true)
		EncodeInts(e, make([]int16, 20))
	})
	d := NewDecoder(append(value, 0x2a))
	d.Skip()
	if v := d.Uint(); v != 0x2a {
		t.Fatalf("value after Skip = %d (%v)", v, d.Err())
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}

	for _, in := range []string{"", "c1", "92 01", "dd ffffffff", "df ffffffff", "c6 ffffffff", "81 01"} {
		d := NewDecoder(mustHex(t, in))
		d.Skip()
		if d.Finish() == nil {
			t.Errorf("Skip(%q) succeeded", in)
		}
	}
}
