package cocosketch

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

type cell struct {
	row, col int
	key      string
	val      uint64
}

// fromCells builds a rows x cols sketch holding exactly cells.
func fromCells(t *testing.T, rows, cols int, cells []cell) *CocoSketch {
	t.Helper()
	s, err := NewCocoSketch(rows, cols)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cells {
		s.table[c.row][c.col] = cocoBucket{Key: c.key, Val: c.val, HasKey: true}
	}
	return s
}

func sameState(got, want *CocoSketch) bool {
	return got.d == want.d && got.length == want.length &&
		slices.EqualFunc(got.table, want.table, slices.Equal)
}

func TestASAPv1Golden(t *testing.T) {
	known := fromCells(t, 3, 7, []cell{
		{0, 0, "uint32-min", 65536},
		{0, 1, "uint16-max", 65535},
		{0, 2, "fixint-max", 127},
		{0, 3, "", 1},
		{0, 4, "uint8-max", 255},
		{0, 5, "emoji-😀", 5},
		{1, 0, "uint16-min", 256},
		{1, 1, "str8-min-32-bytes-0123456789abcd", 3},
		{1, 2, "uint8-min", 128},
		{1, 3, "clé-ünïcode-流量", 4},
		{1, 4, "fixstr-max-31-bytes-0123456789a", 2},
		{1, 5, "uint64-min", 4294967296},
		{1, 6, "zero", 0},
		{2, 1, "uint64-max", math.MaxUint64},
		{2, 5, "uint32-max", 4294967295},
	})
	asapv1test.CheckGolden(t, "coco_3x7", known, sameState)
}

func TestASAPv1RoundTripAfterInserts(t *testing.T) {
	s, _ := NewCocoSketch(3, 37)
	s.SetSeed(1)
	for i := range 400 {
		s.Insert(fmt.Sprintf("flow::%d", i%90), uint64(i%7+1))
	}
	s.InsertWithHash(0xdeadbeef)
	b, err := s.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var got CocoSketch
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !sameState(&got, s) {
		t.Fatal("decoded table differs")
	}
	for i := range 90 {
		k := fmt.Sprintf("flow::%d", i)
		if got.Estimate(k) != s.Estimate(k) {
			t.Fatalf("Estimate(%q) = %d, want %d", k, got.Estimate(k), s.Estimate(k))
		}
	}
	again, err := got.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, again, b)
}

func TestASAPv1EmptyBucketsAreNil(t *testing.T) {
	s, _ := NewCocoSketch(2, 4)
	b, err := s.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := asapv1.SplitKind(b, asapv1.KindCoco)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x92, 0x98, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0x98, 0, 0, 0, 0, 0, 0, 0, 0}
	asapv1test.Equal(t, payload, want)
}

// envelope frames a Coco envelope from row-major keys (nil for free) and values.
func envelope(t *testing.T, kind asapv1.KindID, rows, cols uint64, keys []*string, vals []uint64) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("rows", rows)
	md.Uint("cols", cols)
	p := asapv1.NewEncoder()
	p.Array(2)
	p.Array(len(keys))
	for _, k := range keys {
		if k == nil {
			p.Nil()
		} else {
			p.Str(*k)
		}
	}
	asapv1.EncodeUints(p, vals)
	b, err := asapv1.Marshal(kind, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ptr(s string) *string { return &s }

func mappedCol(row int, key string, cols int) int {
	s := &CocoSketch{length: cols}
	return s.hashIndex(row, key)
}

func expectDecodeError(t *testing.T, b []byte, want string) {
	t.Helper()
	var s CocoSketch
	err := s.UnmarshalASAPv1(b)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want an error containing %q", err, want)
	}
}

func TestASAPv1RejectsMisplacedKey(t *testing.T) {
	key := "flow::misplaced"
	keys := make([]*string, 2)
	keys[1-mappedCol(0, key, 2)] = ptr(key)
	vals := make([]uint64, 2)
	vals[1-mappedCol(0, key, 2)] = 11
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 1, 2, keys, vals), "maps to column")

	keys[1-mappedCol(0, key, 2)], keys[mappedCol(0, key, 2)] = nil, ptr(key)
	vals[1-mappedCol(0, key, 2)], vals[mappedCol(0, key, 2)] = 0, 11
	var s CocoSketch
	if err := s.UnmarshalASAPv1(envelope(t, asapv1.KindCoco, 1, 2, keys, vals)); err != nil {
		t.Fatalf("correctly placed key: %v", err)
	}
	if got := s.Estimate(key); got != 11 {
		t.Fatalf("Estimate = %d, want 11", got)
	}
}

func TestASAPv1MarshalRejectsMisplacedKey(t *testing.T) {
	key := "flow::misplaced"
	s := fromCells(t, 1, 2, []cell{{0, 1 - mappedCol(0, key, 2), key, 11}})
	if _, err := s.MarshalASAPv1(); err == nil || !strings.Contains(err.Error(), "maps to column") {
		t.Fatalf("Marshal of a misplaced key: got %v", err)
	}
}

func TestASAPv1RejectsDuplicateKey(t *testing.T) {
	key := "flow::twice"
	keys := make([]*string, 4)
	vals := make([]uint64, 4)
	for r := range 2 {
		keys[r*2+mappedCol(r, key, 2)] = ptr(key)
		vals[r*2+mappedCol(r, key, 2)] = 7
	}
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 2, 2, keys, vals), "repeats a key")

	s := fromCells(t, 2, 2, []cell{
		{0, mappedCol(0, key, 2), key, 7},
		{1, mappedCol(1, key, 2), key, 7},
	})
	if _, err := s.MarshalASAPv1(); err == nil || !strings.Contains(err.Error(), "repeats a key") {
		t.Fatalf("Marshal of a duplicated key: got %v", err)
	}
}

func TestASAPv1RejectsMassUnderFreeBucket(t *testing.T) {
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 1, 2, make([]*string, 2), []uint64{0, 9}), "unoccupied")

	s, _ := NewCocoSketch(1, 2)
	s.table[0][1].Val = 9
	if _, err := s.MarshalASAPv1(); err == nil {
		t.Fatal("Marshal of mass under a free bucket succeeded")
	}
}

func TestASAPv1RejectsBadGeometry(t *testing.T) {
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 0, 4, nil, nil), "non-zero")
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 4, 0, nil, nil), "non-zero")
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 21, 1, make([]*string, 21), make([]uint64, 21)), "at most 20 rows")
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 20, 1<<24, make([]*string, 3), make([]uint64, 3)), "keys for")
	expectDecodeError(t, envelope(t, asapv1.KindCoco, 1, 3, make([]*string, 3), make([]uint64, 2)), "values for")

	s, _ := NewCocoSketch(21, 4)
	if _, err := s.MarshalASAPv1(); err == nil {
		t.Fatal("Marshal of 21 rows succeeded")
	}
	ok, _ := NewCocoSketch(20, 4)
	if _, err := ok.MarshalASAPv1(); err != nil {
		t.Fatalf("Marshal of 20 rows: %v", err)
	}
	var zero CocoSketch
	if _, err := zero.MarshalASAPv1(); err == nil {
		t.Fatal("Marshal of a zero sketch succeeded")
	}
}

func TestASAPv1RejectsForeignKindAndMetadata(t *testing.T) {
	expectDecodeError(t, envelope(t, asapv1.KindCountMin, 1, 2, make([]*string, 2), make([]uint64, 2)), "")

	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", 1)
	md.Uint("cols", 1)
	p := asapv1.NewEncoder()
	p.Array(2)
	p.Array(1)
	p.Nil()
	asapv1.EncodeUints(p, []uint64{0})
	b, err := asapv1.Marshal(asapv1.KindCoco, md, p)
	if err != nil {
		t.Fatal(err)
	}
	expectDecodeError(t, b, "matrix_seed_index")
}

func TestASAPv1EmptyKeyIsOccupied(t *testing.T) {
	s := fromCells(t, 1, 3, []cell{{0, mappedCol(0, "", 3), "", 5}})
	b, err := s.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var got CocoSketch
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !sameState(&got, s) || got.Estimate("") != 5 {
		t.Fatalf("empty key did not round-trip: %+v", got.table)
	}
}

func TestASAPv1RejectsInvalidUTF8Key(t *testing.T) {
	key := "\xff"
	s := fromCells(t, 1, 3, []cell{{0, mappedCol(0, key, 3), key, 1}})
	if _, err := s.MarshalASAPv1(); err == nil {
		t.Fatal("Marshal of an invalid UTF-8 key succeeded")
	}
}

func TestInsertFindsKeyPastAFreeBucket(t *testing.T) {
	key := "flow::late"
	s := fromCells(t, 2, 5, []cell{{1, mappedCol(1, key, 5), key, 3}})
	s.Insert(key, 4)
	if got := s.Estimate(key); got != 7 {
		t.Fatalf("Estimate = %d, want 7", got)
	}
	if _, err := s.MarshalASAPv1(); err != nil {
		t.Fatalf("Marshal after insert: %v", err)
	}
}
