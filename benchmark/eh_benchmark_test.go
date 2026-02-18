package benchmark

import (
	"log"
	"strconv"
	"testing"

	"github.com/approx-telemetry/sketchlib-go/common"
	eh "github.com/approx-telemetry/sketchlib-go/sketch_framework/ExponentialHistogram"
	univmon "github.com/approx-telemetry/sketchlib-go/sketch_framework/UnivMon"
	cocosketch "github.com/approx-telemetry/sketchlib-go/sketches/CocoSketch"
	countmin "github.com/approx-telemetry/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/approx-telemetry/sketchlib-go/sketches/DDSketch"
	hll "github.com/approx-telemetry/sketchlib-go/sketches/HLL"
	kll "github.com/approx-telemetry/sketchlib-go/sketches/KLL"
	"github.com/approx-telemetry/sketchlib-go/testdata"
)

const (
	// EH Configuration
	ehK        = 50
	windowSize = 100000

	// Sketch Configurations
	cmRow, cmCol = 5, 2048
	csRow, csCol = 5, 2048
	kllK         = 200
	ddsAlpha     = 0.01
	cocoD, cocoL = 3, 2048
	univK        = 50
)

// Global stream data to prevent I/O during benchmarks
var stream []testdata.Sample

func init() {
	// Adjust these paths to match your actual project structure
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	file2 := "../estdata/caida/equinix-nyc.dirA.20181220-130300.UTC.anon.pcap.gz"

	var err error
	stream, err = testdata.ReadCAIDAStream(file1, file2)
	if err != nil {
		log.Printf("WARNING: Failed to load CAIDA stream: %v. Benchmarks using stream will panic or be empty.", err)
	} else {
		log.Printf("Benchmark: Loaded %d CAIDA packets.", len(stream))
	}
}

// Wrapper interfaces
type EHUpdater interface {
	UpdateItem(key string, t int64) error
	QueryInterval(t1, t2 int64) (common.Sketch, error)
}

type EHValueUpdater interface {
	UpdateValue(val float64, t int64) error
	QueryInterval(t1, t2 int64) (common.Sketch, error)
}

// ==============================================================================
// 1. THROUGHPUT BENCHMARKS (Update Speed with CAIDA Data)
// ==============================================================================

func BenchmarkEH_Throughput_CountMin(b *testing.B) {
	e := eh.NewExpoHistogramCountMin(ehK, windowSize, cmRow, cmCol)
	benchmarkEHUpdateString(b, e)
}

func BenchmarkEH_Throughput_CountSketch(b *testing.B) {
	e := eh.NewExpoHistogramCountSketch(ehK, windowSize, csRow, csCol)
	benchmarkEHUpdateString(b, e)
}

func BenchmarkEH_Throughput_UnivMon(b *testing.B) {
	e := eh.NewExpoHistogramUniv(ehK, windowSize, univK, 5, 200, 3)
	benchmarkEHUpdateString(b, e)
}

func BenchmarkEH_Throughput_Coco(b *testing.B) {
	e := eh.NewExpoHistogramCoco(ehK, windowSize, cocoD, cocoL)
	benchmarkEHUpdateString(b, e)
}

func BenchmarkEH_Throughput_HLL(b *testing.B) {
	e := eh.NewExpoHistogramHLL(ehK, windowSize)
	benchmarkEHUpdateString(b, e)
}

func BenchmarkEH_Throughput_KLL(b *testing.B) {
	e := eh.NewExpoHistogramKLL(ehK, windowSize, kllK)
	benchmarkEHUpdateValue(b, e)
}

func BenchmarkEH_Throughput_DDSketch(b *testing.B) {
	e := eh.NewExpoHistogramDDS(ehK, windowSize, ddsAlpha)
	benchmarkEHUpdateValue(b, e)
}

// --- Helpers for Update using CAIDA Stream ---

func benchmarkEHUpdateString(b *testing.B, e EHUpdater) {
	if len(stream) == 0 {
		b.Skip("CAIDA stream not loaded")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Cycle through the stream
		sample := stream[i%len(stream)]
		// Convert IP (float64) to string key
		key := strconv.FormatUint(uint64(sample.F), 10)
		e.UpdateItem(key, sample.T)
	}
}

func benchmarkEHUpdateValue(b *testing.B, e EHValueUpdater) {
	if len(stream) == 0 {
		b.Skip("CAIDA stream not loaded")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sample := stream[i%len(stream)]
		e.UpdateValue(sample.F, sample.T)
	}
}

// ==============================================================================
// 2. QUERY SPEED BENCHMARKS (QueryInterval)
// ==============================================================================

func BenchmarkEH_Query_CountMin(b *testing.B) {
	e := eh.NewExpoHistogramCountMin(ehK, windowSize, cmRow, cmCol)
	preloadEHString(e)
	benchmarkEHQuery(b, e)
}

func BenchmarkEH_Query_CountSketch(b *testing.B) {
	e := eh.NewExpoHistogramCountSketch(ehK, windowSize, csRow, csCol)
	preloadEHString(e)
	benchmarkEHQuery(b, e)
}

func BenchmarkEH_Query_UnivMon(b *testing.B) {
	e := eh.NewExpoHistogramUniv(ehK, windowSize, univK, 5, 200, 3)
	preloadEHString(e)
	benchmarkEHQuery(b, e)
}

func BenchmarkEH_Query_KLL(b *testing.B) {
	e := eh.NewExpoHistogramKLL(ehK, windowSize, kllK)
	preloadEHValue(e)
	benchmarkEHQueryValue(b, e)
}

// --- Helpers for Query ---

// Preload fills the histogram with a chunk of CAIDA data to ensure structure exists
func preloadEHString(e EHUpdater) {
	if len(stream) == 0 {
		return
	}
	limit := 5000 // Preload 5k items
	if len(stream) < limit {
		limit = len(stream)
	}

	for i := 0; i < limit; i++ {
		sample := stream[i]
		key := strconv.FormatUint(uint64(sample.F), 10)
		e.UpdateItem(key, sample.T)
	}
}

func preloadEHValue(e EHValueUpdater) {
	if len(stream) == 0 {
		return
	}
	limit := 5000
	if len(stream) < limit {
		limit = len(stream)
	}

	for i := 0; i < limit; i++ {
		sample := stream[i]
		e.UpdateValue(sample.F, sample.T)
	}
}

func benchmarkEHQuery(b *testing.B, e EHUpdater) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.QueryInterval(0, windowSize)
	}
}

func benchmarkEHQueryValue(b *testing.B, e EHValueUpdater) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.QueryInterval(0, windowSize)
	}
}

// ==============================================================================
// 3. MERGE SPEED BENCHMARKS (Raw Sketch Merge)
// ==============================================================================

func BenchmarkSketch_Merge_CountMin(b *testing.B) {
	s1, _ := countmin.NewCountMinSketch(cmRow, cmCol)
	s2, _ := countmin.NewCountMinSketch(cmRow, cmCol)
	// Fill s2 with some CAIDA data
	fillSketchString(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_CountSketch(b *testing.B) {
	s1, _ := countsketch.NewCountSketch(csRow, csCol)
	s2, _ := countsketch.NewCountSketch(csRow, csCol)
	fillSketchString(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_KLL(b *testing.B) {
	s1, _ := kll.NewKLLSketch(kllK)
	s2, _ := kll.NewKLLSketch(kllK)
	fillSketchValue(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_HLL(b *testing.B) {
	s1 := hll.NewHyperLogLog()
	s2 := hll.NewHyperLogLog()
	fillSketchString(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_DDSketch(b *testing.B) {
	s1 := ddsketch.NewDDSketch(ddsAlpha)
	s2 := ddsketch.NewDDSketch(ddsAlpha)
	fillSketchValue(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_Coco(b *testing.B) {
	s1, _ := cocosketch.NewCocoSketch(cocoD, cocoL)
	s2, _ := cocosketch.NewCocoSketch(cocoD, cocoL)
	fillSketchString(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

func BenchmarkSketch_Merge_UnivMon(b *testing.B) {
	s1, _ := univmon.NewUnivSketchPyramid(univK, 5, 200, 3)
	s2, _ := univmon.NewUnivSketchPyramid(univK, 5, 200, 3)
	fillSketchString(s2, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s1.Merge(s2)
	}
}

// --- Helper to fill raw sketches ---

func fillSketchString(s common.Sketch, count int) {
	if len(stream) == 0 {
		// Fallback if no CAIDA
		for i := 0; i < count; i++ {
			s.InsertWithHash(uint64(i))
		}
		return
	}

	limit := count
	if len(stream) < limit {
		limit = len(stream)
	}

	for i := 0; i < limit; i++ {
		// Using InsertWithHash to mimic typical generic usage
		// Note: We use the raw uint64 IP as hash here for speed in raw benchmarks
		// (assuming InsertWithHash treats input as hash)
		s.InsertWithHash(uint64(stream[i].F))
	}
}

func fillSketchValue(s interface{}, count int) {
	if len(stream) == 0 {
		// Fallback
		if k, ok := s.(*kll.KLLSketch); ok {
			for i := 0; i < count; i++ {
				k.Insert(float64(i))
			}
		} else if d, ok := s.(*ddsketch.DDSketch); ok {
			for i := 0; i < count; i++ {
				d.Add(float64(i))
			}
		}
		return
	}

	limit := count
	if len(stream) < limit {
		limit = len(stream)
	}

	if k, ok := s.(*kll.KLLSketch); ok {
		for i := 0; i < limit; i++ {
			k.Insert(stream[i].F)
		}
	} else if d, ok := s.(*ddsketch.DDSketch); ok {
		for i := 0; i < limit; i++ {
			d.Add(stream[i].F)
		}
	}
}
