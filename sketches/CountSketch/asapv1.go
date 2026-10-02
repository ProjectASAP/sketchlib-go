package countsketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// MarshalASAPv1 encodes the sketch as an ASAPv1 Count Sketch. It fails where
// wireCells does.
func (s *CountSketch) MarshalASAPv1() ([]byte, error) {
	counts, counterType, mode, err := s.wireCells()
	if err != nil {
		return nil, err
	}
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", uint64(s.Rows))
	md.Uint("cols", uint64(s.Cols))
	md.Str("counter_type", counterType)
	md.Str("mode", mode)
	p := asapv1.NewEncoder()
	p.Array(1)
	asapv1.EncodeInts(p, counts)
	return asapv1.Marshal(asapv1.KindCountSketch, md, p)
}

// wireCells returns the cells row-major with the counter type and mode names.
// It fails for a CounterFloat64 sketch, for a cell that is not an integer
// within the counter type's range, and after a hash-only write that did not follow Mode.
func (s *CountSketch) wireCells() (counts []int64, counterType, mode string, err error) {
	if s.hashWriteForeign {
		return nil, "", "", fmt.Errorf("countsketch: counters include a write from a precomputed hash that does not follow Mode")
	}
	if counterType, err = s.CounterType.wireName(); err != nil {
		return nil, "", "", err
	}
	if mode, err = s.Mode.wireName(); err != nil {
		return nil, "", "", err
	}
	if err := checkWireDims(s.Rows, s.Cols); err != nil {
		return nil, "", "", err
	}
	if len(s.Count) != s.Rows {
		return nil, "", "", fmt.Errorf("countsketch: %d count rows for %d rows", len(s.Count), s.Rows)
	}
	counts = make([]int64, 0, s.Rows*s.Cols)
	for r, row := range s.Count {
		if len(row) != s.Cols {
			return nil, "", "", fmt.Errorf("countsketch: row %d has %d cols, want %d", r, len(row), s.Cols)
		}
		for c, v := range row {
			if !s.CounterType.holds(v) {
				return nil, "", "", fmt.Errorf("countsketch: cell (%d,%d) = %v is not an %s", r, c, v, counterType)
			}
			counts = append(counts, int64(v))
		}
	}
	return counts, counterType, mode, nil
}

// UnmarshalASAPv1 replaces the sketch with the ASAPv1 Count Sketch in b,
// recomputing the per-row L2 sums and starting empty top-k trackers. An i64
// counter must be exact in float64, and cols must be a power of two.
func (s *CountSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindCountSketch)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	rows := md.Uint32("rows")
	cols := md.Uint32("cols")
	counterTypeName := md.Str("counter_type")
	modeName := md.Str("mode")
	if err := md.Finish(); err != nil {
		return err
	}
	counterType, err := parseCounterType(counterTypeName)
	if err != nil {
		return err
	}
	mode, err := parseMode(modeName)
	if err != nil {
		return err
	}
	if err := checkWireDims(int(rows), int(cols)); err != nil {
		return err
	}
	p.ExpectArray(1)
	var counts []int64
	if counterType == CounterInt32 {
		for _, v := range asapv1.DecodeInts[int32](p) {
			counts = append(counts, int64(v))
		}
	} else {
		counts = asapv1.DecodeInts[int64](p)
	}
	if err := p.Finish(); err != nil {
		return err
	}
	if uint64(len(counts)) != uint64(rows)*uint64(cols) {
		return fmt.Errorf("countsketch: %d counts for %dx%d", len(counts), rows, cols)
	}
	out, err := NewCountSketch(int(rows), int(cols))
	if err != nil {
		return err
	}
	out.CounterType, out.Mode = counterType, mode
	for i, v := range counts {
		f := float64(v)
		if !counterType.holds(f) || int64(f) != v {
			return fmt.Errorf("countsketch: counter %d is not exact in float64", v)
		}
		r := i / int(cols)
		out.Count[r][i%int(cols)] = f
		out.L2[r] += f * f
	}
	*s = *out
	return nil
}

// checkWireDims accepts 1 <= rows <= len(seed_list) and a power-of-two cols
// that fits uint32.
func checkWireDims(rows, cols int) error {
	if maxRows := len(asapv1.StandardProfile().SeedList); rows < 1 || rows > maxRows {
		return fmt.Errorf("countsketch: rows %d outside [1, %d]", rows, maxRows)
	}
	if cols < 1 || uint64(cols) > math.MaxUint32 || cols&(cols-1) != 0 {
		return fmt.Errorf("countsketch: cols %d is not a power of two that fits uint32", cols)
	}
	return nil
}

func (t CounterType) wireName() (string, error) {
	switch t {
	case CounterInt32:
		return "i32", nil
	case CounterInt64:
		return "i64", nil
	case CounterFloat64:
		return "", fmt.Errorf("countsketch: float64 counters have no ASAPv1 encoding")
	}
	return "", fmt.Errorf("countsketch: unknown CounterType %d", t)
}

func parseCounterType(name string) (CounterType, error) {
	switch name {
	case "i32":
		return CounterInt32, nil
	case "i64":
		return CounterInt64, nil
	}
	return 0, fmt.Errorf("countsketch: counter_type %q is not i32 or i64", name)
}

// holds reports whether v is an integer within t's range.
func (t CounterType) holds(v float64) bool {
	if v != math.Trunc(v) {
		return false
	}
	switch t {
	case CounterInt32:
		return v >= math.MinInt32 && v <= math.MaxInt32
	case CounterInt64:
		return v >= -(1<<63) && v < 1<<63
	}
	return false
}

func (m Mode) wireName() (string, error) {
	switch m {
	case ModeFast:
		return "fast", nil
	case ModeRegular:
		return "regular", nil
	}
	return "", fmt.Errorf("countsketch: unknown Mode %d", m)
}

func parseMode(name string) (Mode, error) {
	switch name {
	case "fast":
		return ModeFast, nil
	case "regular":
		return ModeRegular, nil
	}
	return 0, fmt.Errorf("countsketch: mode %q is not fast or regular", name)
}
