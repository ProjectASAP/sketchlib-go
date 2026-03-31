package benchmark

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	octosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/OctoSketch"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

const (
	octoBenchRows       = 5
	octoBenchCols       = 2048
	octoBenchDDSAlpha   = 0.02
	octoBenchTau        = 128 //128 default
	octoBenchDeltaBuf   = 1 << 15
	octoBenchMergeEvery = 10_000
)

type octoSketchKind string

type octoVariant string

const (
	octoKindCountMin    octoSketchKind = "countmin"
	octoKindCountSketch octoSketchKind = "countsketch"
	octoKindHLL         octoSketchKind = "hll"
	octoKindDDSketch    octoSketchKind = "ddsketch"

	octoVariantIdeal octoVariant = "ideal_single"
	octoVariantMerge octoVariant = "merge_periodic"
	octoVariantOcto  octoVariant = "octosketch"
)

type octoPacket struct {
	keyID uint32
	hash  uint64
	canon uint64
	value float64
	keyIn *common.SketchInput
	ddIn  *common.SketchInput
}

type octoDataset struct {
	name      string
	packets   []octoPacket
	checkpts  []int
	queryKeys []uint32
	// Pre-computed input pointer slices, built once at dataset construction.
	// Eliminates the per-benchmark-op allocation of a fresh selector slice.
	keyInputs []*common.SketchInput
	ddInputs  []*common.SketchInput
	// Value extremes used to pre-warm DDSketch bucket ranges before parallel inserts.
	ddMinValue float64
	ddMaxValue float64
}

// prepareDatasetInputs pre-computes the keyInputs/ddInputs pointer slices and
// the DDSketch value extremes for ds. Call this once after packets are populated.
func prepareDatasetInputs(ds *octoDataset) {
	n := len(ds.packets)
	ds.keyInputs = make([]*common.SketchInput, n)
	ds.ddInputs = make([]*common.SketchInput, n)
	if n == 0 {
		return
	}
	ds.ddMinValue = ds.packets[0].value
	ds.ddMaxValue = ds.packets[0].value
	for i, p := range ds.packets {
		ds.keyInputs[i] = p.keyIn
		ds.ddInputs[i] = p.ddIn
		if p.value < ds.ddMinValue {
			ds.ddMinValue = p.value
		}
		if p.value > ds.ddMaxValue {
			ds.ddMaxValue = p.value
		}
	}
}

type octoMetrics struct {
	AbsError  float64 `json:"abs_error"`
	RelError  float64 `json:"rel_error"`
	Recall    float64 `json:"recall"`
	Precision float64 `json:"precision"`
	F1        float64 `json:"f1"`
	NumQuery  int     `json:"num_query"`
}

type octoLogRecord struct {
	TimestampRFC3339 string         `json:"ts"`
	Dataset          string         `json:"dataset"`
	Sketch           octoSketchKind `json:"sketch"`
	Variant          octoVariant    `json:"variant"`
	Workers          int            `json:"workers"`
	Category         string         `json:"category"`
	Checkpoint       int            `json:"checkpoint"`
	Metrics          *octoMetrics   `json:"metrics,omitempty"`
	Mpps             float64        `json:"mpps,omitempty"`
	CPUUtilPct       float64        `json:"cpu_util_pct,omitempty"`
	BatchLatencyNs   float64        `json:"batch_latency_ns,omitempty"`
	QueryLatencyNs   float64        `json:"query_latency_ns,omitempty"`
}

var (
	octoCAIDAOnce sync.Once
	octoCAIDAData octoDataset
	octoCAIDAErr  error

	octoLogMu sync.Mutex

	octoSummaryMu   sync.Mutex
	octoSummaryRows []octoLogRecord
)

func resetOctoSummary() {
	octoSummaryMu.Lock()
	octoSummaryRows = octoSummaryRows[:0]
	octoSummaryMu.Unlock()
}

func appendOctoSummary(rec octoLogRecord) {
	octoSummaryMu.Lock()
	octoSummaryRows = append(octoSummaryRows, rec)
	octoSummaryMu.Unlock()
}

func snapshotOctoSummary() []octoLogRecord {
	octoSummaryMu.Lock()
	defer octoSummaryMu.Unlock()
	out := make([]octoLogRecord, len(octoSummaryRows))
	copy(out, octoSummaryRows)
	return out
}

func printOctoSummaryTable(tb testing.TB, title string) {
	tb.Helper()
	rows := snapshotOctoSummary()
	if len(rows) == 0 {
		fmt.Printf("\n=== %s ===\n", title)
		fmt.Println("no rows")
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rows[i], rows[j]
		if ri.Category != rj.Category {
			return ri.Category < rj.Category
		}
		if ri.Sketch != rj.Sketch {
			return ri.Sketch < rj.Sketch
		}
		if ri.Variant != rj.Variant {
			return ri.Variant < rj.Variant
		}
		if ri.Workers != rj.Workers {
			return ri.Workers < rj.Workers
		}
		return ri.Checkpoint < rj.Checkpoint
	})

	fmt.Printf("\n=== %s ===\n", title)
	fmt.Println("category           sketch      variant        workers checkpoint  cpu(%)  mpps   qlat(ns)  abs_err   rel_err   recall precision       f1")
	fmt.Println("--------------------------------------------------------------------------------------------------------------------------------")
	for _, r := range rows {
		absErr, relErr, recall, precision, f1 := math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()
		if r.Metrics != nil {
			absErr = r.Metrics.AbsError
			relErr = r.Metrics.RelError
			recall = r.Metrics.Recall
			precision = r.Metrics.Precision
			f1 = r.Metrics.F1
		}
		fmt.Printf("%-18s %-11s %-14s %7d %10d %7.2f %6.2f %9.1f %9s %9s %8s %9s %8s\n",
			r.Category, r.Sketch, r.Variant, r.Workers, r.Checkpoint, r.CPUUtilPct, r.Mpps, r.QueryLatencyNs,
			formatCell(absErr), formatCell(relErr), formatCell(recall), formatCell(precision), formatCell(f1),
		)
	}
}

func formatCell(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "NA"
	}
	return fmt.Sprintf("%.4f", v)
}

func loadCAIDAOctoDataset(tb testing.TB) octoDataset {
	tb.Helper()
	octoCAIDAOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			octoCAIDAErr = err
			return
		}
		if len(samples) == 0 {
			octoCAIDAErr = fmt.Errorf("empty CAIDA dataset")
			return
		}

		packets := make([]octoPacket, len(samples))
		for i, s := range samples {
			k := uint32(s.F)
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], k)
			hash := common.Hash64(b[:])
			canon := common.HashIt(common.CanonicalHashSeed, b[:])
			value := float64(k) + 1
			packets[i] = octoPacket{
				keyID: k,
				hash:  hash,
				canon: canon,
				value: value,
				keyIn: &common.SketchInput{
					Hash:          hash,
					CanonicalHash: canon,
				},
				ddIn: &common.SketchInput{
					Float64:    value,
					HasFloat64: true,
				},
			}
		}
		checkpts := buildCheckpoints(len(packets), []int{1_000_000, 2_000_000, 4_000_000})
		queryKeys := collectQueryKeys(packets, 1024)

		octoCAIDAData = octoDataset{
			name:      "caida",
			packets:   packets,
			checkpts:  checkpts,
			queryKeys: queryKeys,
		}
		prepareDatasetInputs(&octoCAIDAData)
	})
	if octoCAIDAErr != nil {
		tb.Skipf("Skipping OctoSketch CAIDA evaluation: %v", octoCAIDAErr)
	}
	return octoCAIDAData
}

func buildCheckpoints(n int, preferred []int) []int {
	c := make([]int, 0, len(preferred)+1)
	for _, p := range preferred {
		if p > 0 && p <= n {
			c = append(c, p)
		}
	}
	if len(c) == 0 || c[len(c)-1] != n {
		c = append(c, n)
	}
	return c
}

func collectQueryKeys(packets []octoPacket, limit int) []uint32 {
	freq := make(map[uint32]uint64, min(200_000, len(packets)))
	for _, p := range packets {
		freq[p.keyID]++
	}
	type kv struct {
		k uint32
		v uint64
	}
	arr := make([]kv, 0, len(freq))
	for k, v := range freq {
		arr = append(arr, kv{k: k, v: v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].v == arr[j].v {
			return arr[i].k < arr[j].k
		}
		return arr[i].v > arr[j].v
	})
	if len(arr) > limit {
		arr = arr[:limit]
	}
	out := make([]uint32, len(arr))
	for i := range arr {
		out[i] = arr[i].k
	}
	return out
}

func keyInputFromPacket(p octoPacket) common.SketchInput {
	return common.SketchInput{Hash: p.hash, CanonicalHash: p.canon}
}

func ddInputFromPacket(p octoPacket) common.SketchInput {
	return common.SketchInput{Float64: p.value, HasFloat64: true}
}

// benchmarkTauConfig returns a TauConfig calibrated against queueCap — the
// maximum observable queue depth for the τ controller being configured.
// For batched+sharded systems: queueCap = DefaultBatchPoolSize × numWorkers.
// For single unbatched workers: queueCap = deltaCh buffer capacity.
func benchmarkTauConfig(initial float64, queueCap int) octosketch.TauConfig {
	cfg := octosketch.DefaultTauConfig(initial, queueCap)
	cfg.Min = 4
	cfg.Max = initial * 32
	cfg.Step = 1
	cfg.TargetQueue = max(4, queueCap/2)
	cfg.Deadband = max(1, queueCap/4)
	cfg.Interval = 2 * time.Millisecond
	return cfg
}

func onlineAccuracyTauConfig(deltaBufSize int) octosketch.TauConfig {
	cfg := octosketch.DefaultTauConfig(8, deltaBufSize)
	cfg.Min = 1
	cfg.Max = 64
	cfg.Step = 1
	cfg.TargetQueue = max(32, deltaBufSize/16)
	cfg.Deadband = max(8, deltaBufSize/64)
	cfg.Interval = 2 * time.Millisecond
	return cfg
}

func waitForQueueDrain(deltaCh <-chan common.DeltaUpdate) {
	// Best-effort stabilization for checkpoint snapshots in streaming mode.
	for i := 0; i < 200; i++ {
		if len(deltaCh) == 0 {
			time.Sleep(50 * time.Microsecond)
			if len(deltaCh) == 0 {
				return
			}
		}
		time.Sleep(50 * time.Microsecond)
	}
}

func keyInputFromID(id uint32) common.SketchInput {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], id)
	return common.SketchInput{
		Hash:          common.Hash64(b[:]),
		CanonicalHash: common.HashIt(common.CanonicalHashSeed, b[:]),
	}
}

func quantileInput(q float64) common.SketchInput {
	return common.SketchInput{Float64: q, HasFloat64: true}
}

func newSketch(kind octoSketchKind) (any, error) {
	switch kind {
	case octoKindCountMin:
		return countminsketch.NewCountMinSketch(octoBenchRows, octoBenchCols)
	case octoKindCountSketch:
		return countsketch.NewCountSketch(octoBenchRows, octoBenchCols)
	case octoKindHLL:
		return hll.NewHyperLogLog(), nil
	case octoKindDDSketch:
		return ddsketch.NewDDSketch(octoBenchDDSAlpha), nil
	default:
		return nil, fmt.Errorf("unknown sketch kind: %s", kind)
	}
}

func insertPacket(kind octoSketchKind, sketch any, p octoPacket) {
	switch kind {
	case octoKindCountMin:
		in := keyInputFromPacket(p)
		sketch.(*countminsketch.CountMinSketch).Insert(&in)
	case octoKindCountSketch:
		in := keyInputFromPacket(p)
		sketch.(*countsketch.CountSketch).Insert(&in)
	case octoKindHLL:
		in := keyInputFromPacket(p)
		sketch.(*hll.HyperLogLog).Insert(&in)
	case octoKindDDSketch:
		sketch.(*ddsketch.DDSketch).Add(p.value)
	}
}

func estimateKey(kind octoSketchKind, sketch any, key uint32) float64 {
	in := keyInputFromID(key)
	switch kind {
	case octoKindCountMin:
		return sketch.(*countminsketch.CountMinSketch).Estimate(&in)
	case octoKindCountSketch:
		return sketch.(*countsketch.CountSketch).Estimate(&in)
	}
	return 0
}

func estimateHLL(sketch any) float64 {
	return sketch.(*hll.HyperLogLog).Estimate(nil)
}

func estimateDDS(sketch any, q float64) float64 {
	in := quantileInput(q)
	return sketch.(*ddsketch.DDSketch).Estimate(&in)
}

func mergeInto(kind octoSketchKind, dst, src any) {
	switch kind {
	case octoKindCountMin:
		_ = dst.(*countminsketch.CountMinSketch).Merge(src.(*countminsketch.CountMinSketch))
	case octoKindCountSketch:
		_ = dst.(*countsketch.CountSketch).Merge(src.(*countsketch.CountSketch))
	case octoKindHLL:
		_ = dst.(*hll.HyperLogLog).Merge(src.(*hll.HyperLogLog))
	case octoKindDDSketch:
		_ = dst.(*ddsketch.DDSketch).Merge(src.(*ddsketch.DDSketch))
	}
}

func resetSketch(kind octoSketchKind, s any) {
	switch kind {
	case octoKindCountMin:
		s.(*countminsketch.CountMinSketch).Reset()
	case octoKindCountSketch:
		s.(*countsketch.CountSketch).Reset()
	case octoKindHLL:
		s.(*hll.HyperLogLog).Reset()
	}
}

func calculateFreqMetrics(kind octoSketchKind, sketch any, freq map[uint32]uint64, probe []uint32) octoMetrics {
	if len(probe) == 0 {
		return octoMetrics{Recall: math.NaN(), Precision: math.NaN(), F1: math.NaN()}
	}
	var absSum, relSum float64
	truePos := 0
	predPos := 0
	tp := 0

	trueCounts := make([]float64, len(probe))
	for i, k := range probe {
		trueCounts[i] = float64(freq[k])
	}
	sorted := append([]float64(nil), trueCounts...)
	sort.Float64s(sorted)
	kIndex := max(0, len(sorted)-min(64, len(sorted)))
	threshold := sorted[kIndex]

	for _, k := range probe {
		trueV := float64(freq[k])
		est := estimateKey(kind, sketch, k)
		absErr := math.Abs(est - trueV)
		absSum += absErr
		if trueV > 0 {
			relSum += absErr / trueV
		}

		trueHH := trueV >= threshold
		predHH := est >= threshold
		if trueHH {
			truePos++
		}
		if predHH {
			predPos++
		}
		if trueHH && predHH {
			tp++
		}
	}

	precision := safeDiv(float64(tp), float64(predPos))
	recall := safeDiv(float64(tp), float64(truePos))
	f1 := safeF1(precision, recall)
	return octoMetrics{
		AbsError:  absSum / float64(len(probe)),
		RelError:  relSum / float64(len(probe)),
		Recall:    recall,
		Precision: precision,
		F1:        f1,
		NumQuery:  len(probe),
	}
}

func calculateHLLMetrics(sketch any, trueCard int) octoMetrics {
	est := estimateHLL(sketch)
	absErr := math.Abs(est - float64(trueCard))
	rel := 0.0
	if trueCard > 0 {
		rel = absErr / float64(trueCard)
	}
	return octoMetrics{
		AbsError:  absErr,
		RelError:  rel,
		Recall:    math.NaN(),
		Precision: math.NaN(),
		F1:        math.NaN(),
		NumQuery:  1,
	}
}

func calculateDDSMetrics(sketch any, values []float64) octoMetrics {
	if len(values) == 0 {
		return octoMetrics{Recall: math.NaN(), Precision: math.NaN(), F1: math.NaN()}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	queries := []float64{0.50, 0.90, 0.95, 0.99}
	var absSum, relSum float64
	for _, q := range queries {
		trueQ := exactQuantile(sorted, q)
		estQ := estimateDDS(sketch, q)
		absErr := math.Abs(estQ - trueQ)
		absSum += absErr
		if trueQ > 0 {
			relSum += absErr / trueQ
		}
	}

	trueThr := exactQuantile(sorted, 0.99)
	estThr := estimateDDS(sketch, 0.99)
	window := values
	if len(window) > 65_536 {
		window = window[len(window)-65_536:]
	}
	truePos, predPos, tp := 0, 0, 0
	for _, v := range window {
		truth := v >= trueThr
		pred := v >= estThr
		if truth {
			truePos++
		}
		if pred {
			predPos++
		}
		if truth && pred {
			tp++
		}
	}
	precision := safeDiv(float64(tp), float64(predPos))
	recall := safeDiv(float64(tp), float64(truePos))
	return octoMetrics{
		AbsError:  absSum / float64(len(queries)),
		RelError:  relSum / float64(len(queries)),
		Recall:    recall,
		Precision: precision,
		F1:        safeF1(precision, recall),
		NumQuery:  len(queries),
	}
}

func exactQuantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func evaluateOnlineAtCheckpoints(t testing.TB, ds octoDataset, kind octoSketchKind, variant octoVariant, workers int) {
	t.Helper()

	freq := make(map[uint32]uint64, 1<<16)
	unique := make(map[uint32]struct{}, 1<<16)
	values := make([]float64, 0, len(ds.packets))

	nextCheckpoint := 0
	checkpoints := ds.checkpts

	logCheckpoint := func(sketch any, processed int) {
		if nextCheckpoint >= len(checkpoints) || processed != checkpoints[nextCheckpoint] {
			return
		}
		var m octoMetrics
		switch kind {
		case octoKindCountMin, octoKindCountSketch:
			m = calculateFreqMetrics(kind, sketch, freq, ds.queryKeys)
		case octoKindHLL:
			m = calculateHLLMetrics(sketch, len(unique))
		case octoKindDDSketch:
			m = calculateDDSMetrics(sketch, values)
		}
		rec := octoLogRecord{
			TimestampRFC3339: time.Now().Format(time.RFC3339),
			Dataset:          ds.name,
			Sketch:           kind,
			Variant:          variant,
			Workers:          workers,
			Category:         "online_accuracy",
			Checkpoint:       processed,
			Metrics:          &m,
		}
		writeOctoLog(t, rec)
		nextCheckpoint++
	}

	switch variant {
	case octoVariantIdeal:
		sketch, err := newSketch(kind)
		if err != nil {
			t.Fatalf("new sketch: %v", err)
		}
		for i, p := range ds.packets {
			insertPacket(kind, sketch, p)
			freq[p.keyID]++
			unique[p.keyID] = struct{}{}
			values = append(values, p.value)
			logCheckpoint(sketch, i+1)
		}

	case octoVariantMerge:
		if workers < 1 {
			workers = 1
		}
		global, err := newSketch(kind)
		if err != nil {
			t.Fatalf("new global sketch: %v", err)
		}
		local := make([]any, workers)
		for i := range workers {
			local[i], err = newSketch(kind)
			if err != nil {
				t.Fatalf("new local sketch: %v", err)
			}
		}

		mergeLocals := func() {
			for i, s := range local {
				mergeInto(kind, global, s)
				if kind == octoKindDDSketch {
					ns, err := newSketch(kind)
					if err != nil {
						t.Fatalf("new local ddsketch: %v", err)
					}
					local[i] = ns
					continue
				}
				resetSketch(kind, s)
			}
		}

		for i, p := range ds.packets {
			insertPacket(kind, local[i%workers], p)
			if (i+1)%octoBenchMergeEvery == 0 {
				mergeLocals()
			}
			freq[p.keyID]++
			unique[p.keyID] = struct{}{}
			values = append(values, p.value)
			if nextCheckpoint < len(checkpoints) && i+1 == checkpoints[nextCheckpoint] {
				mergeLocals()
				logCheckpoint(global, i+1)
			}
		}
		mergeLocals()

	case octoVariantOcto:
		if workers < 1 {
			workers = 1
		}
		deltaCh := make(chan common.DeltaUpdate, octoBenchDeltaBuf)
		tauCfg := onlineAccuracyTauConfig(octoBenchDeltaBuf)
		adaptiveTau := octosketch.NewAdaptiveTau(tauCfg)
		tauCtrl := octosketch.NewTauController(adaptiveTau, func() int { return len(deltaCh) })
		aggSketch, err := newSketch(kind)
		if err != nil {
			t.Fatalf("new agg sketch: %v", err)
		}
		workerList := make([]*octosketch.Worker, workers)
		for i := range workers {
			workerSketch, err := newSketch(kind)
			if err != nil {
				t.Fatalf("new worker sketch: %v", err)
			}
			workerList[i] = octosketch.NewWorker(i, workerSketch.(octosketch.CellSketch), adaptiveTau, deltaCh, nil)
		}

		var aggMu sync.RWMutex
		aggDone := make(chan struct{})
		go func() {
			for d := range deltaCh {
				aggMu.Lock()
				aggSketch.(octosketch.CellSketch).MergeDelta(d)
				aggMu.Unlock()
			}
			close(aggDone)
		}()
		tauCtrl.Run()

		for i, p := range ds.packets {
			input := p.keyIn
			if kind == octoKindDDSketch {
				input = p.ddIn
			}
			workerList[i%workers].Process(input)

			freq[p.keyID]++
			unique[p.keyID] = struct{}{}
			values = append(values, p.value)

			if nextCheckpoint < len(checkpoints) && i+1 == checkpoints[nextCheckpoint] {
				waitForQueueDrain(deltaCh)
				aggMu.RLock()
				logCheckpoint(aggSketch, i+1)
				aggMu.RUnlock()
			}
		}
		for _, w := range workerList {
			w.Flush()
		}
		tauCtrl.Stop()
		close(deltaCh)
		<-aggDone
	}
}

func measureThroughputAndResource(t testing.TB, ds octoDataset, kind octoSketchKind, variant octoVariant, workers int, includeLatency bool, persistLog bool) octoLogRecord {
	t.Helper()
	if workers < 1 {
		workers = 1
	}
	cpuStart := readCPUSeconds()
	wallStart := time.Now()
	switch variant {
	case octoVariantIdeal:
		sketch, err := newSketch(kind)
		if err != nil {
			t.Fatalf("new sketch: %v", err)
		}
		for _, p := range ds.packets {
			insertPacket(kind, sketch, p)
		}
		if includeLatency {
			_ = measureQueryLatency(kind, sketch, ds)
		}
	case octoVariantMerge:
		global, err := newSketch(kind)
		if err != nil {
			t.Fatalf("new global sketch: %v", err)
		}
		local := make([]any, workers)
		for i := range workers {
			local[i], err = newSketch(kind)
			if err != nil {
				t.Fatalf("new local sketch: %v", err)
			}
		}
		// Pre-warm per-sketch state before spawning goroutines.
		//
		// DDSketch: bucket array grows via ensure() which allocates a new
		// backing slice. Without pre-warming, all workers hit ensure()
		// simultaneously at startup, triggering concurrent GC cycles that cause
		// non-monotonic timing (workers=2 slower than workers=1). Inserting the
		// global min and max values expands each local sketch to the full value
		// range once, serially, so the hot parallel loops see only pre-allocated,
		// in-cache bucket arrays.
		//
		// HLL: register array is fixed at 16 KB (16 384 × uint8), allocated and
		// zero-initialized at construction — no dynamic growth, no GC pressure.
		// The pre-warm here is cache-only: inserting the first packet of each
		// worker's chunk touches the Vector1D slice header and seeds one register
		// cache line into L2 before goroutines compete for the register array.
		// HLL Insert is idempotent (max semantics), so inserting a packet here
		// and again inside the goroutine is safe.
		n := len(ds.packets)
		chunkSize := (n + workers - 1) / workers
		switch kind {
		case octoKindDDSketch:
			if ds.ddMinValue > 0 && ds.ddMaxValue > ds.ddMinValue {
				for _, s := range local {
					s.(*ddsketch.DDSketch).Add(ds.ddMinValue)
					s.(*ddsketch.DDSketch).Add(ds.ddMaxValue)
				}
			}
		case octoKindHLL:
			for wid, s := range local {
				start := wid * chunkSize
				if start < n {
					insertPacket(kind, s, ds.packets[start])
				}
			}
		}
		// Each worker processes a contiguous chunk of the dataset in parallel,
		// then a single serial merge combines local sketches into global.
		// This gives true CPU parallelism instead of the previous single-threaded
		// round-robin loop that never ran more than one goroutine at a time.
		var mergeWG sync.WaitGroup
		mergeWG.Add(workers)
		for wid := range workers {
			go func(wid int) {
				defer mergeWG.Done()
				start := wid * chunkSize
				if start >= n {
					return
				}
				end := min(start+chunkSize, n)
				for i := start; i < end; i++ {
					insertPacket(kind, local[wid], ds.packets[i])
				}
			}(wid)
		}
		mergeWG.Wait()
		for _, s := range local {
			mergeInto(kind, global, s)
		}
		if includeLatency {
			_ = measureQueryLatency(kind, global, ds)
		}
	case octoVariantOcto:
		runOctoConcurrentSystem(t, ds, kind, workers)
	}
	wallDur := time.Since(wallStart)
	cpuDur := readCPUSeconds() - cpuStart

	mpps := float64(len(ds.packets)) / wallDur.Seconds() / 1e6
	cpuUtil := safeDiv(cpuDur, wallDur.Seconds()*float64(max(1, runtime.NumCPU()))) * 100.0
	batchLat := 0.0
	queryLat := 0.0
	if includeLatency {
		batchLat = measureBatchLatency(kind, variant, workers, ds)
		queryLat = measureQueryLatencyStandalone(t, kind, variant, workers, ds)
	}

	rec := octoLogRecord{
		TimestampRFC3339: time.Now().Format(time.RFC3339),
		Dataset:          ds.name,
		Sketch:           kind,
		Variant:          variant,
		Workers:          workers,
		Category:         "throughput_resource",
		Mpps:             mpps,
		CPUUtilPct:       cpuUtil,
		BatchLatencyNs:   batchLat,
		QueryLatencyNs:   queryLat,
	}
	if persistLog {
		writeOctoLog(t, rec)
	} else {
		appendOctoSummary(rec)
	}
	return rec
}

// runOctoConcurrentSystem runs the OctoSketch pipeline for one sketch kind using
// the sketch-specific system constructor. Each constructor uses the batched+sharded
// architecture (64-delta batches, one aggregator shard per worker) for continuous
// aggregation with no shared-channel bottleneck.
//
// Packets are pre-typed and split into contiguous chunks (one per worker), then
// dispatched via RunDirect — eliminating the inputCh channel hop that previously
// added ~100-200 ns of goroutine-boundary overhead per packet.
func runOctoConcurrentSystem(t testing.TB, ds octoDataset, kind octoSketchKind, workers int) {
	t.Helper()
	if workers < 1 {
		workers = 1
	}

	need := workers + 1
	if cur := runtime.GOMAXPROCS(0); cur < need {
		runtime.GOMAXPROCS(need)
	}

	tauCfg := benchmarkTauConfig(octoBenchTau, octosketch.DefaultBatchPoolSize*workers)

	var (
		agg        *octosketch.Aggregator
		workerList []*octosketch.Worker
		deltaCh    chan common.DeltaUpdate
		tauCtrl    *octosketch.TauController
		err        error
	)

	switch kind {
	case octoKindCountMin:
		agg, workerList, _, deltaCh, tauCtrl, err = octosketch.NewCountMinSystemAdaptive(
			octoBenchRows, octoBenchCols, tauCfg, workers, octoBenchDeltaBuf)
	case octoKindCountSketch:
		agg, workerList, _, deltaCh, tauCtrl, err = octosketch.NewCountSketchSystemAdaptive(
			octoBenchRows, octoBenchCols, tauCfg, workers, octoBenchDeltaBuf)
	case octoKindHLL:
		agg, workerList, _, deltaCh, tauCtrl, err = octosketch.NewHLLSystemAdaptive(
			tauCfg, workers, octoBenchDeltaBuf)
	case octoKindDDSketch:
		agg, workerList, _, deltaCh, tauCtrl, err = octosketch.NewDDSketchSystemAdaptive(
			octoBenchDDSAlpha, tauCfg, workers, octoBenchDeltaBuf)
	default:
		t.Fatalf("runOctoConcurrentSystem: unknown sketch kind %q", kind)
	}
	if err != nil {
		t.Fatalf("runOctoConcurrentSystem: failed to create %s system: %v", kind, err)
	}

	// Select the pre-computed input pointer slice for this sketch kind.
	// Built once during dataset construction — zero allocation per benchmark op.
	var allInputs []*common.SketchInput
	if kind == octoKindDDSketch {
		allInputs = ds.ddInputs
	} else {
		allInputs = ds.keyInputs
	}

	// Start the continuously-draining aggregator before any worker emits deltas.
	agg.Run()
	tauCtrl.Run()

	// Assign a contiguous chunk to each worker and process via RunDirect.
	// The worker goroutine pins itself to its coreID, processes the slice,
	// flushes, and closes its batchCh — signalling its aggregator shard to finish.
	n := len(allInputs)
	chunkSize := (n + workers - 1) / workers
	for wid, w := range workerList {
		start := wid * chunkSize
		if start >= n {
			break
		}
		end := min(start+chunkSize, n)
		w.RunDirect(allInputs[start:end])
	}

	for _, w := range workerList {
		<-w.Done()
	}
	tauCtrl.Stop()
	close(deltaCh)
	<-agg.Done()
}

func measureBatchLatency(kind octoSketchKind, variant octoVariant, workers int, ds octoDataset) float64 {
	batch := min(10_000, len(ds.packets))
	if batch == 0 {
		return 0
	}

	start := time.Now()
	switch variant {
	case octoVariantIdeal:
		sketch, _ := newSketch(kind)
		for i := 0; i < batch; i++ {
			insertPacket(kind, sketch, ds.packets[i])
		}
	case octoVariantMerge:
		global, _ := newSketch(kind)
		local := make([]any, workers)
		for i := range workers {
			local[i], _ = newSketch(kind)
		}
		for i := 0; i < batch; i++ {
			insertPacket(kind, local[i%workers], ds.packets[i])
		}
		for _, s := range local {
			mergeInto(kind, global, s)
		}
	case octoVariantOcto:
		sketch, _ := newSketch(kind)
		agg, _ := newSketch(kind)
		deltaCh := make(chan common.DeltaUpdate, octoBenchDeltaBuf)
		tauCfg := benchmarkTauConfig(octoBenchTau, octoBenchDeltaBuf)
		adaptiveTau := octosketch.NewAdaptiveTau(tauCfg)
		tauCtrl := octosketch.NewTauController(adaptiveTau, func() int { return len(deltaCh) })
		w := octosketch.NewWorker(0, sketch.(octosketch.CellSketch), adaptiveTau, deltaCh, nil)
		done := make(chan struct{})
		go func() {
			for d := range deltaCh {
				agg.(octosketch.CellSketch).MergeDelta(d)
			}
			close(done)
		}()
		tauCtrl.Run()
		for i := 0; i < batch; i++ {
			input := ds.packets[i].keyIn
			if kind == octoKindDDSketch {
				input = ds.packets[i].ddIn
			}
			w.Process(input)
		}
		w.Flush()
		tauCtrl.Stop()
		close(deltaCh)
		<-done
	}
	return float64(time.Since(start).Nanoseconds())
}

func measureQueryLatencyStandalone(t testing.TB, kind octoSketchKind, variant octoVariant, workers int, ds octoDataset) float64 {
	t.Helper()
	sketch, err := newSketch(kind)
	if err != nil {
		t.Fatalf("new sketch: %v", err)
	}
	for _, p := range ds.packets {
		insertPacket(kind, sketch, p)
	}
	return measureQueryLatency(kind, sketch, ds)
}

func measureQueryLatency(kind octoSketchKind, sketch any, ds octoDataset) float64 {
	const reps = 2048
	start := time.Now()
	switch kind {
	case octoKindCountMin, octoKindCountSketch:
		if len(ds.queryKeys) == 0 {
			return 0
		}
		for i := 0; i < reps; i++ {
			_ = estimateKey(kind, sketch, ds.queryKeys[i%len(ds.queryKeys)])
		}
	case octoKindHLL:
		for range reps {
			_ = estimateHLL(sketch)
		}
	case octoKindDDSketch:
		qs := []float64{0.5, 0.9, 0.95, 0.99}
		for i := 0; i < reps; i++ {
			_ = estimateDDS(sketch, qs[i%len(qs)])
		}
	}
	return float64(time.Since(start).Nanoseconds()) / reps
}

func writeOctoLog(tb testing.TB, rec octoLogRecord) {
	tb.Helper()
	rec = sanitizeLogRecord(rec)
	line, err := json.Marshal(rec)
	if err != nil {
		tb.Fatalf("marshal log record: %v", err)
	}
	tb.Logf("OCTO_EVAL %s", string(line))
	appendOctoSummary(rec)

	octoLogMu.Lock()
	defer octoLogMu.Unlock()
	path := filepath.Join("benchmark_results", "octosketch_pipeline_eval.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		tb.Logf("octo log file open failed: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func sanitizeLogRecord(rec octoLogRecord) octoLogRecord {
	if math.IsNaN(rec.Mpps) || math.IsInf(rec.Mpps, 0) {
		rec.Mpps = 0
	}
	if math.IsNaN(rec.CPUUtilPct) || math.IsInf(rec.CPUUtilPct, 0) {
		rec.CPUUtilPct = 0
	}
	if math.IsNaN(rec.BatchLatencyNs) || math.IsInf(rec.BatchLatencyNs, 0) {
		rec.BatchLatencyNs = 0
	}
	if math.IsNaN(rec.QueryLatencyNs) || math.IsInf(rec.QueryLatencyNs, 0) {
		rec.QueryLatencyNs = 0
	}
	if rec.Metrics != nil {
		m := *rec.Metrics
		if math.IsNaN(m.AbsError) || math.IsInf(m.AbsError, 0) {
			m.AbsError = 0
		}
		if math.IsNaN(m.RelError) || math.IsInf(m.RelError, 0) {
			m.RelError = 0
		}
		if math.IsNaN(m.Recall) || math.IsInf(m.Recall, 0) {
			m.Recall = 0
		}
		if math.IsNaN(m.Precision) || math.IsInf(m.Precision, 0) {
			m.Precision = 0
		}
		if math.IsNaN(m.F1) || math.IsInf(m.F1, 0) {
			m.F1 = 0
		}
		rec.Metrics = &m
	}
	return rec
}

func readCPUSeconds() float64 {
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		return 0
	}
	user := float64(r.Utime.Sec) + float64(r.Utime.Usec)/1e6
	sys := float64(r.Stime.Sec) + float64(r.Stime.Usec)/1e6
	return user + sys
}

func safeDiv(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}

func safeF1(precision, recall float64) float64 {
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

func buildZipfDataset(name string, n, keySpace int, skew float64, seed int64) octoDataset {
	rng := rand.New(rand.NewSource(seed))
	cdf := make([]float64, keySpace)
	var z float64
	for i := 0; i < keySpace; i++ {
		z += 1.0 / math.Pow(float64(i+1), skew)
		cdf[i] = z
	}
	for i := range cdf {
		cdf[i] /= z
	}

	packets := make([]octoPacket, n)
	for i := range n {
		u := rng.Float64()
		idx := sort.SearchFloat64s(cdf, u)
		if idx >= keySpace {
			idx = keySpace - 1
		}
		k := uint32(idx + 1)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], k)
		hash := common.Hash64(b[:])
		canon := common.HashIt(common.CanonicalHashSeed, b[:])
		value := float64(k) + 1
		packets[i] = octoPacket{
			keyID: k,
			hash:  hash,
			canon: canon,
			value: value,
			keyIn: &common.SketchInput{
				Hash:          hash,
				CanonicalHash: canon,
			},
			ddIn: &common.SketchInput{
				Float64:    value,
				HasFloat64: true,
			},
		}
	}

	ds := octoDataset{
		name:      name,
		packets:   packets,
		checkpts:  buildCheckpoints(n, []int{n / 2, n}),
		queryKeys: collectQueryKeys(packets, 512),
	}
	prepareDatasetInputs(&ds)
	return ds
}

func buildDynamicZipfDataset(n, keySpace int, skews []float64, seed int64) octoDataset {
	rng := rand.New(rand.NewSource(seed))
	packets := make([]octoPacket, 0, n)
	segment := max(1, n/len(skews))
	for segIdx, skew := range skews {
		remain := n - len(packets)
		segmentN := min(segment, remain)
		if segIdx == len(skews)-1 {
			segmentN = remain
		}
		part := buildZipfDataset("", segmentN, keySpace, skew, rng.Int63())
		packets = append(packets, part.packets...)
	}
	ds := octoDataset{
		name:      "zipf_dynamic",
		packets:   packets,
		checkpts:  buildCheckpoints(len(packets), []int{len(packets) / 2, len(packets)}),
		queryKeys: collectQueryKeys(packets, 512),
	}
	prepareDatasetInputs(&ds)
	return ds
}

func BenchmarkOctoSketch_PipelineThroughput_CAIDA(b *testing.B) {
	resetOctoSummary()
	ds := loadCAIDAOctoDataset(b)
	kinds := []octoSketchKind{octoKindCountMin, octoKindCountSketch, octoKindHLL, octoKindDDSketch}
	variants := []octoVariant{octoVariantIdeal, octoVariantMerge, octoVariantOcto}
	workerScales := []int{1, 2, 4, 8, 16}

	for _, kind := range kinds {
		for _, variant := range variants {
			for _, workers := range workerScales {
				if variant == octoVariantIdeal && workers != 1 {
					continue
				}
				name := fmt.Sprintf("%s/%s/workers=%d", kind, variant, workers)
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					var cpuUtilSum float64
					for i := 0; i < b.N; i++ {
						rec := measureThroughputAndResource(b, ds, kind, variant, workers, false, false)
						cpuUtilSum += rec.CPUUtilPct
					}
					if b.N > 0 {
						b.ReportMetric(cpuUtilSum/float64(b.N), "cpu_pct")
					}
				})
			}
		}
	}
	printOctoSummaryTable(b, "OctoSketch Benchmark Summary")
}

func TestOctoSketch_OnlineAccuracy_CAIDA(t *testing.T) {
	requirePaperEvalEnabled(t)
	resetOctoSummary()
	ds := loadCAIDAOctoDataset(t)
	kinds := []octoSketchKind{octoKindCountMin, octoKindCountSketch, octoKindHLL, octoKindDDSketch}
	variants := []octoVariant{octoVariantIdeal, octoVariantMerge, octoVariantOcto}
	workers := []int{1, 2, 4, 8, 16}

	for _, kind := range kinds {
		for _, variant := range variants {
			for _, w := range workers {
				if variant == octoVariantIdeal && w != 1 {
					continue
				}
				evaluateOnlineAtCheckpoints(t, ds, kind, variant, w)
			}
		}
	}
	printOctoSummaryTable(t, "OctoSketch Online Accuracy Summary")
}

func TestOctoSketch_Robustness_Zipf(t *testing.T) {
	requirePaperEvalEnabled(t)
	resetOctoSummary()
	kinds := []octoSketchKind{octoKindCountMin, octoKindCountSketch, octoKindHLL, octoKindDDSketch}
	variants := []octoVariant{octoVariantIdeal, octoVariantMerge, octoVariantOcto}
	workers := []int{4, 8}

	skews := []float64{0.3, 0.7, 1.0, 1.5, 2.0, 3.0}
	for _, skew := range skews {
		ds := buildZipfDataset(fmt.Sprintf("zipf_s%.1f", skew), 300_000, 1<<16, skew, int64(skew*1000)+2026)
		for _, kind := range kinds {
			for _, variant := range variants {
				for _, w := range workers {
					if variant == octoVariantIdeal && w != 4 {
						continue
					}
					evaluateOnlineAtCheckpoints(t, ds, kind, variant, w)
					measureThroughputAndResource(t, ds, kind, variant, w, true, true)
				}
			}
		}
	}

	dyn := buildDynamicZipfDataset(400_000, 1<<16, []float64{0.3, 1.0, 2.0, 3.0}, 20260326)
	for _, kind := range kinds {
		for _, variant := range variants {
			for _, w := range workers {
				if variant == octoVariantIdeal && w != 4 {
					continue
				}
				evaluateOnlineAtCheckpoints(t, dyn, kind, variant, w)
				measureThroughputAndResource(t, dyn, kind, variant, w, true, true)
			}
		}
	}
	printOctoSummaryTable(t, "OctoSketch Robustness Summary")
}

func TestOctoSketch_RuntimeStats(t *testing.T) {
	requirePaperEvalEnabled(t)
	resetOctoSummary()
	ds := loadCAIDAOctoDataset(t)
	kinds := []octoSketchKind{octoKindCountMin, octoKindCountSketch, octoKindHLL, octoKindDDSketch}
	variants := []octoVariant{octoVariantIdeal, octoVariantMerge, octoVariantOcto}
	workers := []int{1, 4, 8, 16}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	writeOctoLog(t, octoLogRecord{
		TimestampRFC3339: time.Now().Format(time.RFC3339),
		Dataset:          ds.name,
		Category:         "runtime_mem_snapshot",
		Mpps:             float64(m.Alloc),
		CPUUtilPct:       float64(m.HeapAlloc),
	})

	for _, kind := range kinds {
		for _, variant := range variants {
			for _, w := range workers {
				if variant == octoVariantIdeal && w != 1 {
					continue
				}
				measureThroughputAndResource(t, ds, kind, variant, w, true, true)
			}
		}
	}
	printOctoSummaryTable(t, "OctoSketch Runtime Summary")
}

func requirePaperEvalEnabled(t testing.TB) {
	t.Helper()
	if os.Getenv("OCTO_PAPER_EVAL") == "1" {
		return
	}
	t.Skip("Skipping long OctoSketch paper-style evaluation. Set OCTO_PAPER_EVAL=1 to run.")
}
