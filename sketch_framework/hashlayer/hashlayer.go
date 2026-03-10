package hashlayer

import "github.com/ProjectASAP/sketchlib-go/common"

// HashLayer computes hash once and dispatches to sketches.
type HashLayer struct {
	sketches []common.Sketch
}

func New(sketches ...common.Sketch) *HashLayer {
	return &HashLayer{sketches: sketches}
}

// Insert is the ONLY entry point for inserting SketchInput.
func (hl *HashLayer) Insert(input *common.SketchInput) {
	h := input.Hash
	for _, s := range hl.sketches {
		s.InsertWithHash(h)
	}
}

// Query queries all sketches using the same hash.
func (hl *HashLayer) Query(
	q common.QueryType,
	input *common.SketchInput,
) ([]float64, []error) {
	h := input.Hash
	results := make([]float64, len(hl.sketches))
	errs := make([]error, len(hl.sketches))

	for i, s := range hl.sketches {
		results[i], errs[i] = s.QueryWithHash(q, h)
	}

	return results, errs
}
