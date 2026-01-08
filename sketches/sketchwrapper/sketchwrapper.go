package sketch

import (
	"errors"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// AnySketch is a thin runtime wrapper.
type AnySketch struct {
	s common.Sketch
}

func New(s common.Sketch) *AnySketch {
	return &AnySketch{s: s}
}

func (a *AnySketch) InsertWithHash(h uint64) {
	a.s.InsertWithHash(h)
}

func (a *AnySketch) QueryWithHash(
	q common.QueryType,
	h uint64,
) (float64, error) {
	return a.s.QueryWithHash(q, h)
}

func (a *AnySketch) Merge(other common.Sketch) error {
	if other == nil {
		return errors.New("nil other")
	}
	return a.s.Merge(other)
}

func (a *AnySketch) TypeName() string {
	return a.s.TypeName()
}
