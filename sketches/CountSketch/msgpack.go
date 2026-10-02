package countsketch

import "math"

// integralCellDelta returns df as an int64 when it is an integer within the
// int64 range, and ok=false otherwise.
func integralCellDelta(df float64) (int64, bool) {
	if math.IsNaN(df) || math.IsInf(df, 0) {
		return 0, false
	}
	r := math.Round(df)
	if r != df { // fractional (or beyond f64 integer precision)
		return 0, false
	}
	if r >= math.MaxInt64 || r < math.MinInt64 {
		return 0, false
	}
	return int64(r), true
}
