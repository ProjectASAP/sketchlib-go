package asapv1

import "testing"

func TestRegistry(t *testing.T) {
	if len(kindNames) != 38 {
		t.Fatalf("registry has %d ids, want 38", len(kindNames))
	}
	for kind, name := range kindNames {
		if len(kind) != 2 || name == "" {
			t.Errorf("registry entry %x = %q", string(kind), name)
		}
	}
	cases := map[KindID]string{
		KindHLLClassic:          "01 01 (HLL Classic)",
		KindCountMin:            "02 00 (Count-Min)",
		KindCSHeap:              "0a 00 (CSHeap)",
		KindDDSketch:            "05 01 (DDSketch)",
		KindDDSketchWithScalars: "05 00 (DDSketch with scalars)",
		KindUnivMonQ:            "1a 00 (UnivMon-Q)",
		KindRetired1D:           "1d 00 (retired)",
		"\x1e\x00":              "1e 00 (unregistered)",
		"":                      " (unregistered)",
		KindHydraUnivMon:        "07 04 (Hydra UnivMon)",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
