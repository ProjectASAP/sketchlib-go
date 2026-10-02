package asapv1

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeLayout(t *testing.T) {
	got, err := Encode(KindHLLHIP, []byte("meta"), []byte("pl"))
	if err != nil {
		t.Fatal(err)
	}
	want := mustHex(t, "415341507631 01 02 0103 00000004 00000002 6d657461 706c")
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode = %x, want %x", got, want)
	}
}

func TestSplitRoundTrip(t *testing.T) {
	for _, kind := range []KindID{"", KindCountMin, KindID(strings.Repeat("k", 255))} {
		for _, blocks := range [][2][]byte{{nil, nil}, {[]byte("m"), nil}, {nil, []byte("p")}, {[]byte("metadata"), []byte("payload")}} {
			b, err := Encode(kind, blocks[0], blocks[1])
			if err != nil {
				t.Fatal(err)
			}
			k, m, p, err := Split(b)
			if err != nil {
				t.Fatalf("Split(%x): %v", b, err)
			}
			if k != kind || !bytes.Equal(m, blocks[0]) || !bytes.Equal(p, blocks[1]) {
				t.Fatalf("Split(%x) = %v %q %q", b, k, m, p)
			}
		}
	}
}

func TestSplitIgnoresTrailingBytes(t *testing.T) {
	b, _ := Encode(KindKLL, []byte("m"), []byte("p"))
	k, m, p, err := Split(append(b, 0xff, 0xff))
	if err != nil || k != KindKLL || string(m) != "m" || string(p) != "p" {
		t.Fatalf("Split with trailing bytes = %v %q %q %v", k, m, p, err)
	}
}

func TestSplitRejects(t *testing.T) {
	valid, _ := Encode(KindCountMin, []byte("metadata"), []byte("payload"))
	for n := range len(valid) {
		if _, _, _, err := Split(valid[:n]); err == nil {
			t.Errorf("Split accepted a %d-byte prefix", n)
		}
	}
	mutate := func(i int, v byte) []byte {
		b := bytes.Clone(valid)
		b[i] = v
		return b
	}
	cases := map[string][]byte{
		"bad magic":           mutate(0, 'X'),
		"lowercase magic":     mutate(4, 'V'),
		"version 0":           mutate(6, 0x00),
		"version 2":           mutate(6, 0x02),
		"kind_id_len too big": mutate(7, 0xff),
		"metadata_len huge":   mutate(10, 0xff),
		"payload_len huge":    mutate(14, 0xff),
		"payload_len +1":      mutate(17, valid[17]+1),
	}
	for name, b := range cases {
		if _, _, _, err := Split(b); err == nil {
			t.Errorf("%s: Split accepted %x", name, b)
		}
	}
}

func TestSplitMaxLengthsFailClosed(t *testing.T) {
	b := append([]byte(Magic), Version, 2, 0x02, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	if _, _, _, err := Split(b); err == nil {
		t.Fatal("Split accepted u32::MAX lengths")
	}
}

func TestSplitKind(t *testing.T) {
	b, _ := Encode(KindHLLClassic, []byte("m"), []byte("p"))
	if m, p, err := SplitKind(b, KindHLLClassic); err != nil || string(m) != "m" || string(p) != "p" {
		t.Fatalf("SplitKind = %q %q %v", m, p, err)
	}
	for _, other := range []KindID{KindHLLErtlMLE, KindHLLHIP, "\x01", "\x01\x01\x00"} {
		if _, _, err := SplitKind(b, other); err == nil {
			t.Errorf("SplitKind accepted %v as %v", KindHLLClassic, other)
		}
	}
	if _, _, err := SplitKind(b[:5], KindHLLClassic); err == nil {
		t.Error("SplitKind accepted a truncated envelope")
	}
}

func TestEncodeRejectsLongKind(t *testing.T) {
	if _, err := Encode(KindID(strings.Repeat("k", 256)), nil, nil); err == nil {
		t.Fatal("Encode accepted a 256-byte kind_id")
	}
}

func TestOpen(t *testing.T) {
	md := NewMetadataWriter()
	md.Uint("k", 7)
	p := NewEncoder()
	p.Array(1)
	p.Uint(9)
	b, _ := Encode(KindKLL, md.Bytes(), p.Bytes())

	r, d, err := Open(b, KindKLL)
	if err != nil {
		t.Fatal(err)
	}
	if k := r.Uint64("k"); k != 7 || r.Finish() != nil {
		t.Fatalf("metadata k = %d (%v)", k, r.Err())
	}
	d.ExpectArray(1)
	if v := d.Uint(); v != 9 || d.Finish() != nil {
		t.Fatalf("payload = %d (%v)", v, d.Err())
	}

	if _, _, err := Open(b, KindKLLDynamic); err == nil {
		t.Error("Open accepted the wrong kind_id")
	}
	bad, _ := Encode(KindKLL, []byte{0x90}, p.Bytes())
	if _, _, err := Open(bad, KindKLL); err == nil {
		t.Error("Open accepted an array as metadata")
	}
}
