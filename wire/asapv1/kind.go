package asapv1

import (
	"fmt"
	"strings"
)

// KindID is an envelope kind_id: the registry bytes naming a sketch algorithm,
// `[family, variant]` for every id allocated so far.
type KindID string

// The kind_id registry. An id is allocated once and never reassigned; ids
// without a designed payload are reserved.
const (
	KindHLLUnspecified       KindID = "\x01\x00" // reserved
	KindHLLClassic           KindID = "\x01\x01"
	KindHLLErtlMLE           KindID = "\x01\x02"
	KindHLLHIP               KindID = "\x01\x03"
	KindCountMin             KindID = "\x02\x00"
	KindCMSHeap              KindID = "\x03\x00"
	KindCountSketch          KindID = "\x04\x00"
	KindDDSketch             KindID = "\x05\x00"
	KindKLL                  KindID = "\x06\x00"
	KindKLLDynamic           KindID = "\x06\x01"
	KindHydraKLL             KindID = "\x07\x00"
	KindHydraCountMin        KindID = "\x07\x01"
	KindHydraCountSketch     KindID = "\x07\x02"
	KindHydraHLL             KindID = "\x07\x03"
	KindHydraUnivMon         KindID = "\x07\x04"
	KindSetAggregator        KindID = "\x08\x00" // reserved
	KindDeltaResult          KindID = "\x09\x00" // reserved
	KindCSHeap               KindID = "\x0a\x00"
	KindElastic              KindID = "\x0b\x00"
	KindCoco                 KindID = "\x0c\x00"
	KindUniformSampling      KindID = "\x0d\x00"
	KindKMV                  KindID = "\x0e\x00"
	KindHashSketchEnsemble   KindID = "\x0f\x00" // reserved
	KindUnivMon              KindID = "\x10\x00"
	KindUnivMonOptimized     KindID = "\x11\x00"
	KindNitroBatch           KindID = "\x12\x00" // reserved
	KindExponentialHistogram KindID = "\x13\x00"
	KindEHSketchList         KindID = "\x14\x00"
	KindEHUnivOptimized      KindID = "\x15\x00" // reserved
	KindOctoSketch           KindID = "\x16\x00" // reserved
	KindBloom                KindID = "\x17\x00"
	KindSpaceSaving          KindID = "\x18\x00"
	KindCountL2HH            KindID = "\x19\x00"
	KindUnivMonQ             KindID = "\x1a\x00"
	KindFoldCMS              KindID = "\x1b\x00" // reserved
	KindFoldCS               KindID = "\x1c\x00" // reserved
	KindRetired1D            KindID = "\x1d\x00" // retired, never reassigned
)

var kindNames = map[KindID]string{
	KindHLLUnspecified:       "HLL Unspecified",
	KindHLLClassic:           "HLL Classic",
	KindHLLErtlMLE:           "HLL Ertl-MLE",
	KindHLLHIP:               "HLL HIP",
	KindCountMin:             "Count-Min",
	KindCMSHeap:              "CMSHeap",
	KindCountSketch:          "Count Sketch",
	KindDDSketch:             "DDSketch",
	KindKLL:                  "KLL",
	KindKLLDynamic:           "KLL Dynamic",
	KindHydraKLL:             "Hydra KLL",
	KindHydraCountMin:        "Hydra Count-Min",
	KindHydraCountSketch:     "Hydra Count Sketch",
	KindHydraHLL:             "Hydra HLL",
	KindHydraUnivMon:         "Hydra UnivMon",
	KindSetAggregator:        "SetAggregator",
	KindDeltaResult:          "DeltaResult",
	KindCSHeap:               "CSHeap",
	KindElastic:              "Elastic",
	KindCoco:                 "Coco",
	KindUniformSampling:      "UniformSampling",
	KindKMV:                  "KMV",
	KindHashSketchEnsemble:   "HashSketchEnsemble",
	KindUnivMon:              "UnivMon",
	KindUnivMonOptimized:     "UnivMon Optimized",
	KindNitroBatch:           "NitroBatch",
	KindExponentialHistogram: "ExponentialHistogram",
	KindEHSketchList:         "EHSketchList",
	KindEHUnivOptimized:      "EHUnivOptimized",
	KindOctoSketch:           "OctoSketch",
	KindBloom:                "Bloom",
	KindSpaceSaving:          "Space-Saving",
	KindCountL2HH:            "CountL2HH",
	KindUnivMonQ:             "UnivMon-Q",
	KindFoldCMS:              "FoldCMS",
	KindFoldCS:               "FoldCS",
	KindRetired1D:            "retired",
}

// Name returns the registry name of k, or "" if k is not in the registry.
func (k KindID) Name() string { return kindNames[k] }

// String renders k as hex bytes followed by its registry name.
func (k KindID) String() string {
	parts := make([]string, len(k))
	for i := range len(k) {
		parts[i] = fmt.Sprintf("%02x", k[i])
	}
	hex := strings.Join(parts, " ")
	if name := k.Name(); name != "" {
		return hex + " (" + name + ")"
	}
	return hex + " (unregistered)"
}
