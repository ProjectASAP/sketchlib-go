package ddsketch

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

type legacyIndexMapping struct {
	gamma       float64
	invLogGamma float64
}

func newLegacyIndexMapping(alpha float64) legacyIndexMapping {
	gamma := (1 + alpha) / (1 - alpha)
	return legacyIndexMapping{
		gamma:       gamma,
		invLogGamma: 1 / math.Log(gamma),
	}
}

func (m legacyIndexMapping) Index(v float64) int32 {
	return int32(math.Floor(math.Log(v) * m.invLogGamma))
}

func (m legacyIndexMapping) Value(k int32) float64 {
	return math.Pow(m.gamma, float64(k)+0.5)
}

type legacyBuckets struct {
	counts []uint64
	offset int32
}

func (b *legacyBuckets) Range() (int32, int32, bool) {
	if len(b.counts) == 0 {
		return 0, 0, false
	}
	left := b.offset
	right := b.offset + int32(len(b.counts)) - 1
	return left, right, true
}

func (b *legacyBuckets) ensure(k int32) {
	if len(b.counts) == 0 {
		b.counts = make([]uint64, GrowChunk)
		b.offset = k - int32(GrowChunk/2)
		return
	}
	left, right, _ := b.Range()
	if k < left {
		needed := int(left - k)
		grow := needed
		if grow < GrowChunk {
			grow = GrowChunk
		}
		newCounts := make([]uint64, grow+len(b.counts))
		copy(newCounts[grow:], b.counts)
		b.counts = newCounts
		b.offset -= int32(grow)
	} else if k > right {
		needed := int(k - right)
		grow := needed
		if grow < GrowChunk {
			grow = GrowChunk
		}
		b.counts = append(b.counts, make([]uint64, grow)...)
	}
}

func (b *legacyBuckets) addOne(k int32) {
	idx := k - b.offset
	if idx >= 0 {
		i := int(idx)
		if i < len(b.counts) {
			b.counts[i]++
			return
		}
	}
	b.ensure(k)
	b.counts[int(k-b.offset)]++
}

type legacyDDSketch struct {
	mapping legacyIndexMapping
	store   legacyBuckets

	count uint64
	sum   float64
	min   float64
	max   float64
}

func newLegacyDDSketch(alpha float64) *legacyDDSketch {
	return &legacyDDSketch{
		mapping: newLegacyIndexMapping(alpha),
		min:     math.Inf(1),
		max:     math.Inf(-1),
	}
}

func (d *legacyDDSketch) Add(v float64) {
	if !(v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)) {
		return
	}
	d.count++
	d.sum += v
	if v < d.min {
		d.min = v
	}
	if v > d.max {
		d.max = v
	}
	k := d.mapping.Index(v)
	d.store.addOne(k)
}

func (d *legacyDDSketch) InsertWithHash(hash uint64) {
	d.Add(float64(hash))
}

func (d *legacyDDSketch) GetValueAtQuantile(q float64) (float64, bool) {
	if d.count == 0 || q < 0 || q > 1 {
		return 0, false
	}
	if q == 0 {
		return d.min, true
	}
	if q == 1 {
		return d.max, true
	}
	rank := uint64(math.Ceil(q * float64(d.count)))
	var seen uint64
	for i, c := range d.store.counts {
		if c == 0 {
			continue
		}
		seen += c
		if seen >= rank {
			k := d.store.offset + int32(i)
			v := d.mapping.Value(k)
			if v < d.min {
				v = d.min
			}
			if v > d.max {
				v = d.max
			}
			return v, true
		}
	}
	return d.max, true
}

var (
	ddCaidaHashesOnce sync.Once
	ddCaidaHashesData []uint64
	ddCaidaHashesErr  error
)

func loadCAIDAHashesForDDSBenchmark(b *testing.B) []uint64 {
	b.Helper()
	ddCaidaHashesOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			ddCaidaHashesErr = err
			return
		}
		hashes := make([]uint64, len(samples))
		buf := make([]byte, 4)
		for i, s := range samples {
			binary.BigEndian.PutUint32(buf, uint32(s.F))
			hashes[i] = common.Hash64(buf)
		}
		ddCaidaHashesData = hashes
	})
	if ddCaidaHashesErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", ddCaidaHashesErr)
	}
	if len(ddCaidaHashesData) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return ddCaidaHashesData
}

func BenchmarkDDSketch_Compare_Insert_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForDDSBenchmark(b)
	n := len(hashes)
	const alpha = 0.01

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Initializing legacy sketch (manual slices)")
		s := newLegacyDDSketch(alpha)
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Initializing new sketch (vector wrapper)")
		s := NewDDSketch(alpha)
		b.Log("[STEP 3] Running insert benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.InsertWithHash(hashes[i%n])
		}
	})
}

func BenchmarkDDSketch_Compare_Query_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForDDSBenchmark(b)
	const alpha = 0.01
	qBits := math.Float64bits(0.99)

	b.Run("legacy", func(b *testing.B) {
		b.Log("[STEP 2] Building and pre-filling legacy sketch")
		legacy := newLegacyDDSketch(alpha)
		for _, h := range hashes {
			legacy.InsertWithHash(h)
		}
		b.Log("[STEP 3] Running query quantile benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = legacy.GetValueAtQuantile(math.Float64frombits(qBits))
		}
	})

	b.Run("new", func(b *testing.B) {
		b.Log("[STEP 2] Building and pre-filling new sketch")
		current := NewDDSketch(alpha)
		for _, h := range hashes {
			current.InsertWithHash(h)
		}
		b.Log("[STEP 3] Running query quantile benchmark")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = current.QueryWithHash(common.QueryQuantile, qBits)
		}
	})
}

func BenchmarkDDSketch_Compare_CreateMatrix2D_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForDDSBenchmark(b)
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

func BenchmarkDDSketch_Compare_CreateMatrix3D_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	hashes := loadCAIDAHashesForDDSBenchmark(b)
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
