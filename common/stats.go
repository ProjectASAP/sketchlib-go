package common

import "sort"

// ComputeMedianInlineF64 computes a median without heap allocation for the
// row counts commonly used by Count/Count-Min sketches.
func ComputeMedianInlineF64(values []float64) float64 {
	switch len(values) {
	case 0:
		return 0
	case 1:
		return values[0]
	case 2:
		return (values[0] + values[1]) / 2
	case 3:
		v0, v1, v2 := values[0], values[1], values[2]
		if v0 > v1 {
			v0, v1 = v1, v0
		}
		if v1 > v2 {
			v1 = v2
		}
		if v0 > v1 {
			v1 = v0
		}
		return v1
	case 4:
		v0, v1, v2, v3 := values[0], values[1], values[2], values[3]
		if v0 > v1 {
			v0, v1 = v1, v0
		}
		if v2 > v3 {
			v2, v3 = v3, v2
		}
		if v0 > v2 {
			v2 = v0
		}
		if v1 > v3 {
			v1 = v3
		}
		return (v1 + v2) / 2
	case 5:
		v0, v1, v2, v3, v4 := values[0], values[1], values[2], values[3], values[4]
		if v0 > v1 {
			v0, v1 = v1, v0
		}
		if v3 > v4 {
			v3, v4 = v4, v3
		}
		if v0 > v3 {
			v0, v3 = v3, v0
		}
		if v1 > v4 {
			v1, v4 = v4, v1
		}
		if v1 > v2 {
			v1, v2 = v2, v1
		}
		if v2 > v3 {
			v2, v3 = v3, v2
		}
		if v1 > v2 {
			v2 = v1
		}
		return v2
	default:
		sort.Float64s(values)
		mid := len(values) / 2
		if len(values)%2 == 1 {
			return values[mid]
		}
		return (values[mid-1] + values[mid]) / 2
	}
}
