package benchmark

import (
	"bufio"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"

	// Import the Exponential Histogram package
	eh "github.com/approx-telemetry/sketchlib-go/sketch_framework/ExponentialHistogram"

	// Import specific sketches for type assertions and internal metrics
	univmon "github.com/approx-telemetry/sketchlib-go/sketch_framework/UnivMon"
	countsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountSketch"
	hll "github.com/approx-telemetry/sketchlib-go/sketches/HLL"
	kll "github.com/approx-telemetry/sketchlib-go/sketches/KLL"
)

// ==============================================================================
// BENCHMARK CONFIG & HELPERS
// ==============================================================================

const (
	BENCH_WINDOW_SIZE = 100000
	BENCH_TIME_RANGE  = 200000
	VALUE_SCALE       = 100000.0
)

type BenchSample struct {
	T int64
	F float64
	S string
}

type ResultRow struct {
	SketchType    string
	WindowSize    int64
	K             int
	Param         string
	AvgInsertTime float64 // ns
	AvgQueryTime  float64 // us
	AvgError      float64 // %
	MemoryKB      float64
}

func genZipfData(n int64) []BenchSample {
	rand.Seed(time.Now().UnixNano())
	vec := make([]BenchSample, 0, n)
	s := 1.01
	v := 1.0
	z := rand.NewZipf(rand.New(rand.NewSource(time.Now().UnixNano())), s, v, uint64(VALUE_SCALE))

	for t := int64(0); t < n; t++ {
		val := float64(z.Uint64()) + 1
		vec = append(vec, BenchSample{
			T: t,
			F: val,
			S: strconv.FormatFloat(val, 'f', -1, 64),
		})
	}
	return vec
}

func genUniformData(n int64) []BenchSample {
	rand.Seed(time.Now().UnixNano())
	vec := make([]BenchSample, 0, n)
	for t := int64(0); t < n; t++ {
		val := rand.Float64() * VALUE_SCALE
		vec = append(vec, BenchSample{
			T: t,
			F: val,
			S: strconv.FormatFloat(val, 'f', -1, 64),
		})
	}
	return vec
}

func calcErr(est, gt float64) float64 {
	if gt == 0 {
		return math.Abs(est)
	}
	return math.Abs(est-gt) / gt
}

// ==============================================================================
// BENCHMARK WORKER FUNCTIONS
// ==============================================================================

// --- KLL ---
func runBenchKLL(k int, kllK int, data []BenchSample, w *bufio.Writer) ResultRow {
	// Accessing via 'eh' package alias
	hist := eh.NewExpoHistogramKLL(k, BENCH_WINDOW_SIZE, kllK)

	var totalInsertTime int64
	var totalQueryTime int64
	var totalError float64
	var queryCount int64

	for i, d := range data {
		start := time.Now()
		hist.UpdateValue(d.F, d.T)
		totalInsertTime += time.Since(start).Nanoseconds()

		if int64(i) >= BENCH_WINDOW_SIZE && i%1000 == 0 {
			startQ := time.Now()
			sketch, err := hist.QueryInterval(d.T-BENCH_WINDOW_SIZE, d.T)
			qDuration := time.Since(startQ)
			totalQueryTime += qDuration.Microseconds()

			if err == nil {
				// Type assertion to access specific KLL methods
				kllS := sketch.(*kll.KLLSketch)
				est := kllS.CDF().Query(0.5)

				windowSlice := make([]float64, 0, BENCH_WINDOW_SIZE)
				for j := int64(i) - BENCH_WINDOW_SIZE; j < int64(i); j++ {
					windowSlice = append(windowSlice, data[j].F)
				}
				sort.Float64s(windowSlice)
				gt := windowSlice[len(windowSlice)/2]

				totalError += calcErr(est, gt)
				queryCount++
			}
		}
	}

	return ResultRow{
		SketchType:    "KLL",
		WindowSize:    BENCH_WINDOW_SIZE,
		K:             k,
		Param:         fmt.Sprintf("kll_k=%d", kllK),
		AvgInsertTime: float64(totalInsertTime) / float64(len(data)),
		AvgQueryTime:  float64(totalQueryTime) / float64(queryCount),
		AvgError:      (totalError / float64(queryCount)) * 100,
		MemoryKB:      0,
	}
}

// --- UNIVMON ---
func runBenchUniv(k int, univK int, data []BenchSample, w *bufio.Writer) ResultRow {
	hist := eh.NewExpoHistogramUniv(k, BENCH_WINDOW_SIZE, univK, 5, 1024, 4)

	var totalInsertTime int64
	var totalQueryTime int64
	var totalError float64
	var queryCount int64
	var lastMem float64

	for i, d := range data {
		start := time.Now()
		hist.UpdateItem(d.S, d.T)
		totalInsertTime += time.Since(start).Nanoseconds()

		if int64(i) >= BENCH_WINDOW_SIZE && i%1000 == 0 {
			startQ := time.Now()
			sketch, err := hist.QueryInterval(d.T-BENCH_WINDOW_SIZE, d.T)
			qDuration := time.Since(startQ)
			totalQueryTime += qDuration.Microseconds()

			if err == nil {
				univ := sketch.(*univmon.UnivSketch)
				est := univ.GetCardinality()
				lastMem = univ.GetMemoryKB()

				unique := make(map[float64]bool)
				for j := int64(i) - BENCH_WINDOW_SIZE; j < int64(i); j++ {
					unique[data[j].F] = true
				}
				gt := float64(len(unique))

				totalError += calcErr(est, gt)
				queryCount++
			}
		}
	}

	return ResultRow{
		SketchType:    "UnivMon",
		WindowSize:    BENCH_WINDOW_SIZE,
		K:             k,
		Param:         fmt.Sprintf("univ_k=%d", univK),
		AvgInsertTime: float64(totalInsertTime) / float64(len(data)),
		AvgQueryTime:  float64(totalQueryTime) / float64(queryCount),
		AvgError:      (totalError / float64(queryCount)) * 100,
		MemoryKB:      lastMem,
	}
}

// --- HLL ---
func runBenchHLL(k int, data []BenchSample, w *bufio.Writer) ResultRow {
	hist := eh.NewExpoHistogramHLL(k, BENCH_WINDOW_SIZE)

	var totalInsertTime int64
	var totalQueryTime int64
	var totalError float64
	var queryCount int64

	for i, d := range data {
		start := time.Now()
		hist.UpdateItem(d.S, d.T)
		totalInsertTime += time.Since(start).Nanoseconds()

		if int64(i) >= BENCH_WINDOW_SIZE && i%2000 == 0 {
			startQ := time.Now()
			sketch, err := hist.QueryInterval(d.T-BENCH_WINDOW_SIZE, d.T)
			qDuration := time.Since(startQ)
			totalQueryTime += qDuration.Microseconds()

			if err == nil {
				hllS := sketch.(*hll.HyperLogLog)
				est := float64(hllS.Estimate())

				unique := make(map[float64]bool)
				for j := int64(i) - BENCH_WINDOW_SIZE; j < int64(i); j++ {
					unique[data[j].F] = true
				}
				gt := float64(len(unique))

				totalError += calcErr(est, gt)
				queryCount++
			}
		}
	}

	return ResultRow{
		SketchType:    "HLL",
		WindowSize:    BENCH_WINDOW_SIZE,
		K:             k,
		Param:         "Standard",
		AvgInsertTime: float64(totalInsertTime) / float64(len(data)),
		AvgQueryTime:  float64(totalQueryTime) / float64(queryCount),
		AvgError:      (totalError / float64(queryCount)) * 100,
		MemoryKB:      2.5,
	}
}

// --- COUNT SKETCH ---
func runBenchCountSketch(k int, rows, cols int, data []BenchSample, w *bufio.Writer) ResultRow {
	hist := eh.NewExpoHistogramCountSketch(k, BENCH_WINDOW_SIZE, rows, cols)
	targetKey := "1"

	var totalInsertTime int64
	var totalQueryTime int64
	var totalError float64
	var queryCount int64

	for i, d := range data {
		start := time.Now()
		hist.UpdateItem(d.S, d.T)
		totalInsertTime += time.Since(start).Nanoseconds()

		if int64(i) >= BENCH_WINDOW_SIZE && i%2000 == 0 {
			startQ := time.Now()
			sketch, err := hist.QueryInterval(d.T-BENCH_WINDOW_SIZE, d.T)
			qDuration := time.Since(startQ)
			totalQueryTime += qDuration.Microseconds()

			if err == nil {
				cs := sketch.(*countsketch.CountSketch)
				hash := common.FromString(targetKey).Hash
				est, _ := cs.QueryWithHash(common.QueryFrequency, hash)

				gt := 0.0
				for j := int64(i) - BENCH_WINDOW_SIZE; j < int64(i); j++ {
					if data[j].S == targetKey {
						gt++
					}
				}

				totalError += calcErr(est, gt)
				queryCount++
			}
		}
	}

	memEst := float64(rows*cols*8) / 1024.0

	return ResultRow{
		SketchType:    "CountSketch",
		WindowSize:    BENCH_WINDOW_SIZE,
		K:             k,
		Param:         fmt.Sprintf("%dx%d", rows, cols),
		AvgInsertTime: float64(totalInsertTime) / float64(len(data)),
		AvgQueryTime:  float64(totalQueryTime) / float64(queryCount),
		AvgError:      (totalError / float64(queryCount)) * 100,
		MemoryKB:      memEst,
	}
}

// ==============================================================================
// MAIN BENCHMARK RUNNER
// ==============================================================================

func TestPerformance_AllSketches(t *testing.T) {
	// Note: Run with 'go test -v -timeout 30m ./benchmark' to execute.
	if testing.Short() {
		t.Skip("Skipping heavy benchmark in short mode")
	}
	// Uncomment line below to skip unless explicitly needed
	// t.Skip("Skipping heavy benchmark. Comment this line to run.")

	runtime.GOMAXPROCS(runtime.NumCPU())

	os.MkdirAll("benchmark_results", os.ModePerm)
	f, err := os.OpenFile("benchmark_results/performance_metrics.csv", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	fmt.Fprintf(w, "Sketch,WindowSize,EH_K,Param,AvgInsert(ns),AvgQuery(us),AvgError(%%),Memory(KB)\n")
	w.Flush()

	fmt.Println(">>> Generating Data Sets...")
	uniformData := genUniformData(BENCH_TIME_RANGE)
	zipfData := genZipfData(BENCH_TIME_RANGE)
	fmt.Println(">>> Data Ready. Starting Benchmarks...")

	ehK_Values := []int{10, 50, 100}
	univMonEH_Values := []int{100, 200}

	var wg sync.WaitGroup
	resultsChan := make(chan ResultRow, 50)

	// --- Benchmark KLL ---
	for _, k := range ehK_Values {
		wg.Add(1)
		go func(k_val int) {
			defer wg.Done()
			fmt.Printf("Running KLL (k=%d)...\n", k_val)
			res := runBenchKLL(k_val, 128, uniformData, w)
			resultsChan <- res
		}(k)
	}

	// --- Benchmark UnivMon ---
	for _, k := range univMonEH_Values {
		wg.Add(1)
		go func(k_val int) {
			defer wg.Done()
			fmt.Printf("Running UnivMon (k=%d)...\n", k_val)
			res := runBenchUniv(k_val, 200, zipfData, w)
			resultsChan <- res
		}(k)
	}

	// --- Benchmark HLL ---
	for _, k := range ehK_Values {
		wg.Add(1)
		go func(k_val int) {
			defer wg.Done()
			fmt.Printf("Running HLL (k=%d)...\n", k_val)
			res := runBenchHLL(k_val, zipfData, w)
			resultsChan <- res
		}(k)
	}

	// --- Benchmark CountSketch ---
	for _, k := range ehK_Values {
		wg.Add(1)
		go func(k_val int) {
			defer wg.Done()
			fmt.Printf("Running CountSketch (k=%d)...\n", k_val)
			res := runBenchCountSketch(k_val, 5, 2048, zipfData, w)
			resultsChan <- res
		}(k)
	}

	// Collector Routine
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Write results as they come in
	for res := range resultsChan {
		fmt.Printf("DONE: %s (k=%d) -> Insert: %.0fns, Query: %.0fus, Err: %.2f%%\n",
			res.SketchType, res.K, res.AvgInsertTime, res.AvgQueryTime, res.AvgError)

		fmt.Fprintf(w, "%s,%d,%d,%s,%.2f,%.2f,%.4f,%.2f\n",
			res.SketchType, res.WindowSize, res.K, res.Param,
			res.AvgInsertTime, res.AvgQueryTime, res.AvgError, res.MemoryKB)
		w.Flush()
	}

	fmt.Println(">>> All Benchmarks Complete. Results saved to benchmark_results/performance_metrics.csv")
}
