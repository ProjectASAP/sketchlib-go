package countsketch

import (
	"encoding/binary"
	"sort"
	"sync"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/approx-telemetry/sketchlib-go/common/storage"
	"github.com/approx-telemetry/sketchlib-go/testdata"
)

type legacyCountSketch struct {
	rows int
	cols int

	count [][]float64
	l2    []float64

	bitsPerRow uint
	mask       uint64
}

func newLegacyCountSketch(rows, cols int) *legacyCountSketch {
	s := &legacyCountSketch{
		rows: rows,
		cols: cols,
		l2:   make([]float64, rows),
		mask: uint64(cols - 1),
	}
	for u := cols - 1; u > 0; u >>= 1 {
		s.bitsPerRow++
	}
	s.count = make([][]float64, rows)
	for r := 0; r < rows; r++ {
		s.count[r] = make([]float64, cols)
	}
	return s
}

func (s *legacyCountSketch) derivePosAndSign(hash uint64, row int) (int, float64) {
	shift := uint(row) * s.bitsPerRow
	col := int((hash >> shift) & s.mask)
	signBit := (hash >> uint(63-row)) & 1
	sign := -1.0
	if signBit == 1 {
		sign = 1.0
	}
	return col, sign
}

func (s *legacyCountSketch) InsertWithHash(hash uint64) {
	for r := 0; r < s.rows; r++ {
		c, sign := s.derivePosAndSign(hash, r)
		prev := s.count[r][c]
		s.count[r][c] += sign
		curr := s.count[r][c]
		s.l2[r] += (curr * curr) - (prev * prev)
	}
}

func (s *legacyCountSketch) QueryFrequency(hash uint64) float64 {
	est := make([]float64, s.rows)
	for r := 0; r < s.rows; r++ {
		c, sign := s.derivePosAndSign(hash, r)
		est[r] = s.count[r][c] * sign
	}
	sort.Float64s(est)
	mid := s.rows / 2
	if s.rows%2 == 1 {
		return est[mid]
	}
	return (est[mid-1] + est[mid]) / 2.0
}

var (
	csCaidaHashesOnce sync.Once
	csCaidaHashesData []uint64
	csCaidaHashesErr  error
)

func loadCAIDAHashesForBenchmark(b *testing.B) []uint64 {
	b.Helper()
	csCaidaHashesOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			csCaidaHashesErr = err
			return
		}
		hashes := make([]uint64, len(samples))
		buf := make([]byte, 4)
		for i, s := range samples {
			binary.BigEndian.PutUint32(buf, uint32(s.F))
			hashes[i] = common.Hash64(buf)
		}
		csCaidaHashesData = hashes
	})
	if csCaidaHashesErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", csCaidaHashesErr)
	}
	if len(csCaidaHashesData) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return csCaidaHashesData
}

func BenchmarkCountSketch_Compare_Insert_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	n := len(hashes)
	const rows, cols = 5, 2048

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Initializing legacy sketch (manual slices)")
		s := newLegacyCountSketch(rows, cols)
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Initializing new sketch (vector storage)")
		s, err := NewCountSketch(rows, cols)
		if err != nil {
			b.Fatalf("new countsketch: %v", err)
		}
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})
}

func BenchmarkCountSketch_Compare_Query_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForBenchmark(b)
	const rows, cols = 5, 2048
	n := len(hashes)

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Building and pre-filling legacy sketch")
		s := newLegacyCountSketch(rows, cols)
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
		s, err := NewCountSketch(rows, cols)
		if err != nil {
			b.Fatalf("new countsketch: %v", err)
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

func BenchmarkCountSketch_Compare_CreateMatrix2D_CAIDA(b *testing.B) {
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

func BenchmarkCountSketch_Compare_CreateMatrix3D_CAIDA(b *testing.B) {
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
