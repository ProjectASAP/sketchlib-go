package hydrasketch

import (
	"slices"
	"testing"

	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
)

func TestCountMinCellState(t *testing.T) {
	s, err := countminsketch.NewCountMinSketch(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.Count[0][1], s.Count[1][0] = 3, -2
	s.Sum[0][1], s.Sum2[1][1], s.L1[0] = 4, 5, 3

	st := countMinCellState(s)
	if st.GetRows() != 2 || st.GetCols() != 2 || st.GetCounterType() != commonpb.CounterType_COUNTER_TYPE_INT64 ||
		!slices.Equal(st.GetCountsInt(), []int64{0, 3, -2, 0}) || st.GetCountsFloat() != nil ||
		!slices.Equal(st.GetSumCounts(), []float64{0, 4, 0, 0}) ||
		!slices.Equal(st.GetSum2Counts(), []float64{0, 0, 0, 5}) ||
		!slices.Equal(st.GetL1(), []float64{3, 0}) {
		t.Fatalf("integral cells: %v", st)
	}

	s.Count[1][1] = 0.5
	st = countMinCellState(s)
	if st.GetCounterType() != commonpb.CounterType_COUNTER_TYPE_FLOAT64 || st.GetCountsInt() != nil ||
		!slices.Equal(st.GetCountsFloat(), []float64{0, 3, -2, 0.5}) {
		t.Fatalf("fractional cell: %v", st)
	}
}
