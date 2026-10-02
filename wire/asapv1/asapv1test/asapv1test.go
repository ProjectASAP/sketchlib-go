// Package asapv1test loads the ASAPv1 golden byte-vectors mounted at the
// repository's asapv1_golden/ submodule and checks codecs against them.
package asapv1test

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// Dir returns the absolute path of the asapv1_golden/ directory.
func Dir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("asapv1test: cannot locate source file")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "asapv1_golden")
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("asapv1test: golden fixtures missing at %s (run `git submodule update --init`): %v", dir, err)
	}
	return dir
}

// Golden returns the bytes of the fixture <name>.hex.
func Golden(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(Dir(t), name+".hex"))
	if err != nil {
		t.Fatalf("asapv1test: %v", err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("asapv1test: fixture %s: %v", name, err)
	}
	return b
}

// Names returns the names of all fixtures, sorted.
func Names(t testing.TB) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(Dir(t), "*.hex"))
	if err != nil {
		t.Fatalf("asapv1test: %v", err)
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = strings.TrimSuffix(filepath.Base(p), ".hex")
	}
	slices.Sort(names)
	return names
}

// Equal fails t unless got equals want, reporting the first differing offset.
func Equal(t testing.TB, got, want []byte) {
	t.Helper()
	if bytes.Equal(got, want) {
		return
	}
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	t.Fatalf("bytes differ at offset %d (got %d bytes, want %d)\n got: %x\nwant: %x",
		i, len(got), len(want), window(got, i), window(want, i))
}

func window(b []byte, i int) []byte {
	return b[max(0, min(i, len(b))-8):min(len(b), i+24)]
}

// CheckMarshal fails t unless m marshals to the fixture name.
func CheckMarshal(t testing.TB, name string, m asapv1.Marshaler) {
	t.Helper()
	got, err := m.MarshalASAPv1()
	if err != nil {
		t.Fatalf("%s: MarshalASAPv1: %v", name, err)
	}
	Equal(t, got, Golden(t, name))
}

// CheckRoundTrip unmarshals the fixture name into u and fails t unless u
// re-marshals to the same bytes.
func CheckRoundTrip(t testing.TB, name string, u interface {
	asapv1.Marshaler
	asapv1.Unmarshaler
}) {
	t.Helper()
	want := Golden(t, name)
	if err := u.UnmarshalASAPv1(want); err != nil {
		t.Fatalf("%s: UnmarshalASAPv1: %v", name, err)
	}
	got, err := u.MarshalASAPv1()
	if err != nil {
		t.Fatalf("%s: re-MarshalASAPv1: %v", name, err)
	}
	Equal(t, got, want)
}
