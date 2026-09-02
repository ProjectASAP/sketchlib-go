package asapmsgpack

import "testing"

func TestCountSketchDeltaSparseRoundTrip(t *testing.T) {
	rowIdx := []uint32{0, 1, 4, 200}
	colIdx := []uint32{3, 3, 0, 255}
	vals := []float64{10.5, -2.25, 1e6, -7.0}
	buf, err := MarshalCountSketchDeltaSparse(5, 256, rowIdx, colIdx, vals)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r, c, ri, ci, vs, err := UnmarshalCountSketchDeltaSparse(buf)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r != 5 || c != 256 {
		t.Fatalf("dims: %d×%d", r, c)
	}
	if len(ri) != 4 || len(ci) != 4 || len(vs) != 4 {
		t.Fatalf("lengths: %d/%d/%d", len(ri), len(ci), len(vs))
	}
	for i := range vals {
		if ri[i] != rowIdx[i] || ci[i] != colIdx[i] || vs[i] != vals[i] {
			t.Fatalf("cell %d: (%d,%d,%v) want (%d,%d,%v)", i, ri[i], ci[i], vs[i], rowIdx[i], colIdx[i], vals[i])
		}
	}
}

func TestCountSketchDeltaSparseEmpty(t *testing.T) {
	buf, err := MarshalCountSketchDeltaSparse(5, 256, nil, nil, nil)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	_, _, ri, _, _, err := UnmarshalCountSketchDeltaSparse(buf)
	if err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if len(ri) != 0 {
		t.Fatalf("empty delta should have 0 cells, got %d", len(ri))
	}
}

func TestCountSketchDeltaSparseLengthMismatch(t *testing.T) {
	_, err := MarshalCountSketchDeltaSparse(5, 256, []uint32{0}, []uint32{0, 1}, []float64{1})
	if err == nil {
		t.Fatal("expected length-mismatch error")
	}
}
