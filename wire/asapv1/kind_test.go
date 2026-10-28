package asapv1

import (
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	if len(registry) != 52 {
		t.Fatalf("registry has %d ids, want 52", len(registry))
	}
	payloads := 0
	for kind, e := range registry {
		if len(kind) != 2 || e.name == "" {
			t.Fatalf("registry entry %x = %+v", string(kind), e)
		}
		if e.payload {
			payloads++
		}
		if delta := kind[1]&0x80 != 0; delta != strings.HasSuffix(e.name, " delta") ||
			delta && (!e.payload || e.name != registry[KindID([]byte{kind[0], kind[1] &^ 0x80})].name+" delta") {
			t.Errorf("%v is not the delta of a registered state kind", kind)
		}
	}
	if payloads != 43 {
		t.Errorf("registry has %d ids with a payload, want 43", payloads)
	}
	cases := map[KindID]string{
		KindHLLClassic:               "01 01 (HLL Classic)",
		KindCountMin:                 "02 00 (Count-Min)",
		KindCSHeap:                   "0a 00 (CSHeap)",
		KindUnivMonQ:                 "1a 00 (UnivMon-Q)",
		KindRetired09:                "09 00 (retired)",
		KindRetired1D:                "1d 00 (retired)",
		KindDDSketch:                 "05 01 (DDSketch)",
		KindDDSketchWithScalars:      "05 00 (DDSketch with scalars)",
		KindDDSketchWithScalarsDelta: "05 80 (DDSketch with scalars delta)",
		KindSpaceSaving:              "18 01 (Space-Saving)",
		KindSpaceSavingWithTotal:     "18 00 (Space-Saving with total)",
		KindCMSSpaceSavingWithTotal:  "1e 00 (CMSSpaceSaving with total)",
		KindCSSpaceSaving:            "1f 01 (CSSpaceSaving)",
		"\x20\x00":                   "20 00 (unregistered)",
		"":                           " (unregistered)",
		KindHydraUnivMon:             "07 04 (Hydra UnivMon)",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
