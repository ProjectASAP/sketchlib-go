package countminsketch

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/approx-telemetry/sketchlib-go/common/storage"
	"github.com/approx-telemetry/sketchlib-go/testdata"
)

type legacyCountMinSketch struct {
	rows int
	cols int

	count [][]float64
	sum   [][]float64
	sum2  [][]float64

	bitsPerRow uint
	mask       uint64
}

func newLegacyCountMinSketch(rows, cols int) *legacyCountMinSketch {
	m := &legacyCountMinSketch{
		rows: rows,
		cols: cols,
		mask: uint64(cols - 1),
	}
	for u := cols - 1; u > 0; u >>= 1 {
		m.bitsPerRow++
	}
	m.count = make([][]float64, rows)
	m.sum = make([][]float64, rows)
	m.sum2 = make([][]float64, rows)
	for r := 0; r < rows; r++ {
		m.count[r] = make([]float64, cols)
		m.sum[r] = make([]float64, cols)
		m.sum2[r] = make([]float64, cols)
	}
	return m
}

func (m *legacyCountMinSketch) deriveIndex(hash uint64, row int) int {
	shift := uint(row) * m.bitsPerRow
	return int((hash >> shift) & m.mask)
}

func (m *legacyCountMinSketch) InsertWithHash(hash uint64) {
	for r := 0; r < m.rows; r++ {
		c := m.deriveIndex(hash, r)
		m.count[r][c] += 1
		m.sum[r][c] += 1
		m.sum2[r][c] += 1
	}
}

func (m *legacyCountMinSketch) QueryFrequency(hash uint64) float64 {
	res := math.MaxFloat64
	for r := 0; r < m.rows; r++ {
		c := m.deriveIndex(hash, r)
		v := m.count[r][c]
		if v < res {
			res = v
		}
	}
	return res
}

var (
	caidaHashesOnce sync.Once
	caidaHashesData []uint64
	caidaHashesErr  error
)

func loadCAIDAHashesForBenchmark(b *testing.B) []uint64 {
	b.Helper()
	caidaHashesOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			caidaHashesErr = err
			return
		}
		hashes := make([]uint64, len(samples))
		buf := make([]byte, 4)
		for i, s := range samples {
			binary.BigEndian.PutUint32(buf, uint32(s.F))
			hashes[i] = common.Hash64(buf)
		}
		caidaHashesData = hashes
	})
	if caidaHashesErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", caidaHashesErr)
	}
	if len(caidaHashesData) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return caidaHashesData
}

func BenchmarkCountMinSketch_Compare_Insert_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	n := len(hashes)
	const rows, cols = 5, 2048

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Initializing legacy sketch (manual slices)")
		s := newLegacyCountMinSketch(rows, cols)
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Initializing new sketch (vector storage)")
		s, err := NewCountMinSketch(rows, cols)
		if err != nil {
			b.Fatalf("new countmin: %v", err)
		}
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})
}

func BenchmarkCountMinSketch_Compare_Query_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	const rows, cols = 5, 2048
	n := len(hashes)

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Building and pre-filling legacy sketch")
		s := newLegacyCountMinSketch(rows, cols)
		for _, h := range hashes {
			s.InsertWithHash(h)
		}
		b.Log("[STEP 3] Running query benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = s.QueryFrequency(hashes[i%n])
		}
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Building and pre-filling new sketch")
		s, err := NewCountMinSketch(rows, cols)
		if err != nil {
			b.Fatalf("new countmin: %v", err)
		}
		for _, h := range hashes {
			s.InsertWithHash(h)
		}
		b.Log("[STEP 3] Running query benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = s.QueryWithHash(common.QueryFrequency, hashes[i%n])
		}
	})
}

func BenchmarkCountMinSketch_Compare_CreateMatrix2D_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	const rows, cols = 5, 2048
	n := len(hashes)

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Running manual [][]float64 matrix creation benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		var sink float64
		for i := 0; i < b.N; i++ {
			m := make([][]float64, rows)
			for r := 0; r < rows; r++ {
				m[r] = make([]float64, cols)
			}
			c := int(hashes[i%n] & uint64(cols-1))
			m[0][c] = 1
			sink += m[0][c]
		}
		_ = sink
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Running Vector2D matrix creation benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		var sink float64
		for i := 0; i < b.N; i++ {
			m, err := storage.InitVector2D[float64](rows, cols)
			if err != nil {
				b.Fatalf("init vector2d: %v", err)
			}
			c := int(hashes[i%n] & uint64(cols-1))
			m.Set(0, c, 1)
			sink += m.At(0, c)
		}
		_ = sink
	})
}

func BenchmarkCountMinSketch_Compare_CreateMatrix3D_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	const layer, row, col = 4, 5, 2048
	n := len(hashes)

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Running manual [][][]float64 matrix creation benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		var sink float64
		for i := 0; i < b.N; i++ {
			m := make([][][]float64, layer)
			for l := 0; l < layer; l++ {
				m[l] = make([][]float64, row)
				for r := 0; r < row; r++ {
					m[l][r] = make([]float64, col)
				}
			}
			c := int(hashes[i%n] & uint64(col-1))
			m[0][0][c] = 1
			sink += m[0][0][c]
		}
		_ = sink
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Running Vector3D matrix creation benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		var sink float64
		for i := 0; i < b.N; i++ {
			m, err := storage.InitVector3D[float64](layer, row, col)
			if err != nil {
				b.Fatalf("init vector3d: %v", err)
			}
			c := int(hashes[i%n] & uint64(col-1))
			m.Set(0, 0, c, 1)
			sink += m.At(0, 0, c)
		}
		_ = sink
	})
}
