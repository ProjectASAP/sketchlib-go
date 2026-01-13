package countminsketch

import (
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
)

func TestCountMinSketchCorrectness(t *testing.T) {
	const (
		rows  = 5
		cols  = 1024 // power-of-two
		items = 10_000
	)

	cms, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatalf("failed to create CMS: %v", err)
	}

	// Ground truth frequency map
	exact := make(map[uint64]int)

	// Insert data:
	// keys in range [0, 99] with varying frequencies
	for i := 0; i < items; i++ {
		key := uint64(i % 100)
		exact[key]++

		in := common.FromU64(key)
		cms.InsertWithHash(in.Hash)
	}

	totalInserts := items
	epsilon := 1.0 / float64(cols)
	maxAllowedError := int(epsilon * float64(totalInserts))

	for key, trueFreq := range exact {
		in := common.FromU64(key)

		est, err := cms.QueryWithHash(common.QueryFrequency, in.Hash)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}

		estimate := int(est)

		// CMS must NEVER underestimate
		if estimate < trueFreq {
			t.Fatalf(
				"underestimation detected: key=%d true=%d est=%d",
				key, trueFreq, estimate,
			)
		}

		// CMS overestimation must be bounded
		if estimate > trueFreq+maxAllowedError {
			t.Fatalf(
				"overestimation too large: key=%d true=%d est=%d max=%d",
				key, trueFreq, estimate, trueFreq+maxAllowedError,
			)
		}
	}
}
