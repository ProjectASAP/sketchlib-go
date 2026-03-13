package benchmark

import "math"

func benchPercentileInt64(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(values)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func benchMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
