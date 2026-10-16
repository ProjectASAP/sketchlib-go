package asapv1

import (
	"fmt"
	"strings"
)

// KindID is an envelope kind_id: the registry bytes naming a sketch algorithm,
// `[family, variant]` for every id allocated so far.
type KindID string

// The kind_id registry. An id is allocated once and never reassigned. A
// variant with its top bit set names the delta of the state variant without it.
const (
	KindHLLUnspecified       KindID = "\x01\x00"
	KindHLLClassic           KindID = "\x01\x01"
	KindHLLErtlMLE           KindID = "\x01\x02"
	KindHLLHIP               KindID = "\x01\x03"
	KindHLLClassicDelta      KindID = "\x01\x81"
	KindHLLErtlMLEDelta      KindID = "\x01\x82"
	KindCountMin             KindID = "\x02\x00"
	KindCountMinDelta        KindID = "\x02\x80"
	KindCMSHeap              KindID = "\x03\x00"
	KindCMSHeapDelta         KindID = "\x03\x80"
	KindCountSketch          KindID = "\x04\x00"
	KindCountSketchDelta     KindID = "\x04\x80"
	KindDDSketch             KindID = "\x05\x00"
	KindDDSketchDelta        KindID = "\x05\x80"
	KindKLL                  KindID = "\x06\x00"
	KindKLLDynamic           KindID = "\x06\x01"
	KindHydraKLL             KindID = "\x07\x00"
	KindHydraCountMin        KindID = "\x07\x01"
	KindHydraCountSketch     KindID = "\x07\x02"
	KindHydraHLL             KindID = "\x07\x03"
	KindHydraUnivMon         KindID = "\x07\x04"
	KindSetAggregator        KindID = "\x08\x00"
	KindSetAggregatorDelta   KindID = "\x08\x80"
	KindRetired09            KindID = "\x09\x00"
	KindCSHeap               KindID = "\x0a\x00"
	KindCSHeapDelta          KindID = "\x0a\x80"
	KindElastic              KindID = "\x0b\x00"
	KindCoco                 KindID = "\x0c\x00"
	KindUniformSampling      KindID = "\x0d\x00"
	KindKMV                  KindID = "\x0e\x00"
	KindHashSketchEnsemble   KindID = "\x0f\x00"
	KindUnivMon              KindID = "\x10\x00"
	KindUnivMonOptimized     KindID = "\x11\x00"
	KindNitroBatch           KindID = "\x12\x00"
	KindExponentialHistogram KindID = "\x13\x00"
	KindEHSketchList         KindID = "\x14\x00"
	KindEHUnivOptimized      KindID = "\x15\x00"
	KindOctoSketch           KindID = "\x16\x00"
	KindBloom                KindID = "\x17\x00"
	KindSpaceSaving          KindID = "\x18\x00"
	KindCountL2HH            KindID = "\x19\x00"
	KindUnivMonQ             KindID = "\x1a\x00"
	KindFoldCMS              KindID = "\x1b\x00"
	KindFoldCS               KindID = "\x1c\x00"
	KindRetired1D            KindID = "\x1d\x00"
)

type kindEntry struct {
	name    string
	payload bool
}

var registry = map[KindID]kindEntry{
	KindHLLUnspecified:       {"HLL Unspecified", false},
	KindHLLClassic:           {"HLL Classic", true},
	KindHLLErtlMLE:           {"HLL Ertl-MLE", true},
	KindHLLHIP:               {"HLL HIP", true},
	KindHLLClassicDelta:      {"HLL Classic delta", true},
	KindHLLErtlMLEDelta:      {"HLL Ertl-MLE delta", true},
	KindCountMin:             {"Count-Min", true},
	KindCountMinDelta:        {"Count-Min delta", true},
	KindCMSHeap:              {"CMSHeap", true},
	KindCMSHeapDelta:         {"CMSHeap delta", true},
	KindCountSketch:          {"Count Sketch", true},
	KindCountSketchDelta:     {"Count Sketch delta", true},
	KindDDSketch:             {"DDSketch", true},
	KindDDSketchDelta:        {"DDSketch delta", true},
	KindKLL:                  {"KLL", true},
	KindKLLDynamic:           {"KLL Dynamic", true},
	KindHydraKLL:             {"Hydra KLL", true},
	KindHydraCountMin:        {"Hydra Count-Min", true},
	KindHydraCountSketch:     {"Hydra Count Sketch", true},
	KindHydraHLL:             {"Hydra HLL", true},
	KindHydraUnivMon:         {"Hydra UnivMon", true},
	KindSetAggregator:        {"SetAggregator", true},
	KindSetAggregatorDelta:   {"SetAggregator delta", true},
	KindRetired09:            {"retired", false},
	KindCSHeap:               {"CSHeap", true},
	KindCSHeapDelta:          {"CSHeap delta", true},
	KindElastic:              {"Elastic", true},
	KindCoco:                 {"Coco", true},
	KindUniformSampling:      {"UniformSampling", true},
	KindKMV:                  {"KMV", true},
	KindHashSketchEnsemble:   {"HashSketchEnsemble", false},
	KindUnivMon:              {"UnivMon", true},
	KindUnivMonOptimized:     {"UnivMon Optimized", true},
	KindNitroBatch:           {"NitroBatch", false},
	KindExponentialHistogram: {"ExponentialHistogram", true},
	KindEHSketchList:         {"EHSketchList", true},
	KindEHUnivOptimized:      {"EHUnivOptimized", false},
	KindOctoSketch:           {"OctoSketch", false},
	KindBloom:                {"Bloom", true},
	KindSpaceSaving:          {"Space-Saving", true},
	KindCountL2HH:            {"CountL2HH", true},
	KindUnivMonQ:             {"UnivMon-Q", true},
	KindFoldCMS:              {"FoldCMS", false},
	KindFoldCS:               {"FoldCS", false},
	KindRetired1D:            {"retired", false},
}

// Name returns the registry name of k, or "" if k is not in the registry.
func (k KindID) Name() string { return registry[k].name }

// hasPayload reports whether k is registered with a designed payload.
func (k KindID) hasPayload() bool { return registry[k].payload }

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
