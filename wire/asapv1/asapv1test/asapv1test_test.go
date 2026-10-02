package asapv1test

import (
	"fmt"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

type fatal struct{}

type recorder struct {
	testing.TB
	msg string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
	panic(fatal{})
}

func failure(t *testing.T, check func(tb testing.TB)) string {
	t.Helper()
	r := &recorder{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatal); !ok {
					panic(v)
				}
			}
		}()
		check(r)
	}()
	return r.msg
}

// golden holds envelope bytes verbatim. Decoding never sets lossy, so a known
// value with lossy set differs from its decoded copy.
type golden struct {
	b     []byte
	lossy bool
}

func (g *golden) MarshalASAPv1() ([]byte, error) { return g.b, nil }

func (g *golden) UnmarshalASAPv1(b []byte) error {
	if _, _, _, err := asapv1.Split(b); err != nil {
		return err
	}
	g.b = append([]byte(nil), b...)
	return nil
}

func TestCheckGolden(t *testing.T) {
	want := Golden(t, "hll_classic_p12")
	if msg := failure(t, func(tb testing.TB) { CheckGolden(tb, "hll_classic_p12", &golden{b: want}, nil) }); msg != "" {
		t.Fatalf("matching codec failed: %s", msg)
	}
	other := Golden(t, "hll_ertl_mle_p12")
	if msg := failure(t, func(tb testing.TB) { CheckGolden(tb, "hll_classic_p12", &golden{b: other}, nil) }); msg == "" {
		t.Error("wrong marshal bytes passed")
	}
	differ := func(got, want *golden) bool { return false }
	if msg := failure(t, func(tb testing.TB) { CheckGolden(tb, "hll_classic_p12", &golden{b: want}, differ) }); msg == "" {
		t.Error("unequal decoded state passed")
	}
	if msg := failure(t, func(tb testing.TB) { CheckGolden(tb, "hll_classic_p12", &golden{b: want, lossy: true}, nil) }); msg == "" {
		t.Error("decoded state differing under DeepEqual passed")
	}
}

func TestEqual(t *testing.T) {
	if msg := failure(t, func(tb testing.TB) { Equal(tb, []byte{1, 2, 3}, []byte{1, 2, 3}) }); msg != "" {
		t.Fatal(msg)
	}
	if msg := failure(t, func(tb testing.TB) { Equal(tb, []byte{1, 2, 3}, []byte{1, 9, 3}) }); msg == "" {
		t.Fatal("unequal bytes passed")
	}
}
