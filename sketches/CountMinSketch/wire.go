package countminsketch

import (
	"errors"
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// The ASAPv1 encoding of a CountMinSketch: float64 counters, columns sliced
// from one matrix hash (mode "fast"), at most one row per standard seed.
const (
	wireCounterType = "f64"
	wireMode        = "fast"
	maxWireRows     = 20
)

func checkWireDims(rows, cols uint64) error {
	if rows == 0 || cols == 0 {
		return fmt.Errorf("countminsketch: ASAPv1 dimensions must be non-zero: rows=%d, cols=%d", rows, cols)
	}
	if rows > maxWireRows {
		return fmt.Errorf("countminsketch: ASAPv1 carries at most %d rows, got %d", maxWireRows, rows)
	}
	if cols > math.MaxUint32 {
		return fmt.Errorf("countminsketch: ASAPv1 cols %d exceeds u32", cols)
	}
	return nil
}

// MarshalASAPv1 encodes the Count matrix as an ASAPv1 Count-Min envelope. It
// rejects WithSampleP-sampled counts (the probability is not carried) and cells
// written by a uint64-hash path outside the packed 64-bit layout.
func (s *CountMinSketch) MarshalASAPv1() ([]byte, error) {
	if s.sampler != nil || s.sampled {
		return nil, errors.New("countminsketch: ASAPv1 cannot encode counts sampled with WithSampleP")
	}
	if s.foreignLayout {
		return nil, fmt.Errorf("countminsketch: ASAPv1 cannot encode a %dx%d sketch written through a uint64-hash path; "+
			"its columns differ from the fast-mode matrix hash outside the packed 64-bit layout", s.Rows, s.Cols)
	}
	if s.Rows < 0 || s.Cols < 0 {
		return nil, fmt.Errorf("countminsketch: negative dimensions %dx%d", s.Rows, s.Cols)
	}
	if err := checkWireDims(uint64(s.Rows), uint64(s.Cols)); err != nil {
		return nil, err
	}
	if len(s.Count) != s.Rows {
		return nil, fmt.Errorf("countminsketch: %d count rows for %d rows", len(s.Count), s.Rows)
	}
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", uint64(s.Rows))
	md.Uint("cols", uint64(s.Cols))
	md.Str("counter_type", wireCounterType)
	md.Str("mode", wireMode)
	p := asapv1.NewEncoder()
	p.Array(1)
	p.Array(s.Rows * s.Cols)
	for r, row := range s.Count {
		if len(row) != s.Cols {
			return nil, fmt.Errorf("countminsketch: count row %d has %d cols, want %d", r, len(row), s.Cols)
		}
		for _, v := range row {
			p.Float64(v)
		}
	}
	return asapv1.Marshal(asapv1.KindCountMin, md, p)
}

// UnmarshalASAPv1 replaces s with the sketch in an ASAPv1 Count-Min envelope.
// Sum, Sum2 and L1 are rebuilt from the counts, so they change across a round
// trip once GOS resets or cell operations have made them diverge.
func (s *CountMinSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindCountMin)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	rows := md.Uint32("rows")
	cols := md.Uint32("cols")
	md.ExpectStr("counter_type", wireCounterType)
	md.ExpectStr("mode", wireMode)
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkWireDims(uint64(rows), uint64(cols)); err != nil {
		return err
	}
	p.ExpectArray(1)
	counts := asapv1.DecodeFloat64s(p)
	if err := p.Finish(); err != nil {
		return err
	}
	if uint64(len(counts)) != uint64(rows)*uint64(cols) {
		return fmt.Errorf("countminsketch: %d counts for %dx%d", len(counts), rows, cols)
	}
	out, err := NewCountMinSketch(int(rows), int(cols))
	if err != nil {
		return err
	}
	for r := range out.Rows {
		row := counts[r*out.Cols : (r+1)*out.Cols]
		copy(out.Count[r], row)
		copy(out.Sum[r], row)
		copy(out.Sum2[r], row)
		for _, v := range row {
			out.L1[r] += v
		}
	}
	*s = *out
	return nil
}
