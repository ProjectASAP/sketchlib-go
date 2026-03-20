package nitrosketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// =====================================================
// Original unit tests
// =====================================================

func TestCountMinBatchInsertFullSampling(t *testing.T) {
	batch, err := NewCountMinBatch(1.0, 3, 8)
	if err != nil {
		t.Fatalf("NewCountMinBatch() error = %v", err)
	}

	key := common.FromU64(42)
	batch.Insert([]*common.SketchInput{key, key, key, key})

	if got := batch.EstimateMedian(key); got != 1 {
		t.Fatalf("EstimateMedian() = %v, want 1", got)
	}

	hashed := common.Hash128It(0, key.Bytes)
	for row := 0; row < 3; row++ {
		col := colForHash128(hashed, row, batch.Target().Sketch.AsStorage().MaskBits(), batch.Target().Sketch.AsStorage().Mask(), batch.Target().Sketch.Cols)
		want := 1.0
		if row == 0 {
			want = 2.0
		}
		if got := batch.Target().Sketch.Count[row][col]; got != want {
			t.Fatalf("row %d counter = %v, want %v", row, got, want)
		}
	}
}

func TestCountSketchBatchInsertFullSampling(t *testing.T) {
	batch, err := NewCountSketchBatch(1.0, 3, 8)
	if err != nil {
		t.Fatalf("NewCountSketchBatch() error = %v", err)
	}

	key := common.FromU64(7)
	batch.Insert([]*common.SketchInput{key, key, key, key})

	if got := batch.EstimateMedian(key); got != 1 {
		t.Fatalf("EstimateMedian() = %v, want 1", got)
	}

	hashed := common.Hash128It(0, key.Bytes)
	for row := 0; row < 3; row++ {
		col := colForHash128(hashed, row, batch.Target().Sketch.AsStorage().MaskBits(), batch.Target().Sketch.AsStorage().Mask(), batch.Target().Sketch.Cols)
		want := signForHash128(hashed, row)
		if row == 0 {
			want *= 2.0
		}
		if got := batch.Target().Sketch.Count[row][col]; got != want {
			t.Fatalf("row %d signed counter = %v, want %v", row, got, want)
		}
	}
}

func TestNitroBatchMergeRequiresMatchingRate(t *testing.T) {
	left, err := NewCountMinBatch(1.0, 3, 8)
	if err != nil {
		t.Fatalf("NewCountMinBatch(left) error = %v", err)
	}
	right, err := NewCountMinBatch(0.5, 3, 8)
	if err != nil {
		t.Fatalf("NewCountMinBatch(right) error = %v", err)
	}

	if err := left.Merge(right); err == nil {
		t.Fatalf("Merge() error = nil, want mismatch error")
	}
}

func TestNitroBatchMergeCountMin(t *testing.T) {
	left, err := NewCountMinBatch(1.0, 3, 8)
	if err != nil {
		t.Fatalf("NewCountMinBatch(left) error = %v", err)
	}
	right, err := NewCountMinBatch(1.0, 3, 8)
	if err != nil {
		t.Fatalf("NewCountMinBatch(right) error = %v", err)
	}

	key := common.FromU64(11)
	left.Insert([]*common.SketchInput{key, key, key})
	right.Insert([]*common.SketchInput{key, key, key})

	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if got := left.EstimateMedian(key); got != 2 {
		t.Fatalf("EstimateMedian() after merge = %v, want 2", got)
	}
}

// =====================================================
// CAIDA dataset helpers
// =====================================================

const (
	caidaFile1 = "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	caidaFile2 = "../../testdata/caida/equinix-nyc.dirA.20181220-130300.UTC.anon.pcap.gz"
	nitroRows  = 5
	nitroCols  = 2048
)

type ipEntry struct {
	ip    uint32
	count uint64
}

// loadCAIDAInputs loads both CAIDA pcap files and returns:
//   - a slice of SketchInputs (one per packet, using BigEndian source IP bytes)
//   - a ground-truth frequency map keyed by source IP as uint32
func loadCAIDAInputs(tb testing.TB) ([]*common.SketchInput, map[uint32]uint64) {
	tb.Helper()
	samples, err := testdata.ReadCAIDAStream(caidaFile1, caidaFile2)
	if err != nil || len(samples) == 0 {
		tb.Skipf("CAIDA dataset unavailable: %v", err)
	}
	inputs := make([]*common.SketchInput, len(samples))
	truth := make(map[uint32]uint64, len(samples)/4)
	for i, s := range samples {
		ip := uint32(s.F)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], ip)
		inputs[i] = common.FromBytes(b[:])
		truth[ip]++
	}
	return inputs, truth
}

func ipKey(ip uint32) *common.SketchInput {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], ip)
	return common.FromBytes(b[:])
}

func sortedByFreq(truth map[uint32]uint64) []ipEntry {
	entries := make([]ipEntry, 0, len(truth))
	for ip, cnt := range truth {
		entries = append(entries, ipEntry{ip, cnt})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})
	return entries
}

// =====================================================
// Correctness test
// =====================================================

// TestNitroCAIDACorrectness verifies structural properties of the sketch on real traffic:
//   - All estimates are non-negative.
//   - The row-assignment scheme (position % rows) is reflected in per-row counters.
//   - The scaled estimate (est × rows) is a non-decreasing function of true frequency
//     across the top heavy hitters.
//
// Note: because this implementation assigns rows via position%rows rather than sampling
// over the flattened packet×row space, EstimateMedian returns ≈ f(x)/rows. The "scaled
// estimate" est×rows is what corresponds to f(x) and is used for all comparisons.
func TestNitroCAIDACorrectness(t *testing.T) {
	inputs, truth := loadCAIDAInputs(t)
	N := len(inputs)

	batch, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	batch.Insert(inputs)

	// 1. All estimates must be non-negative.
	negCount := 0
	for ip := range truth {
		if est := batch.EstimateMedian(ipKey(ip)); est < 0 {
			negCount++
		}
	}
	if negCount > 0 {
		t.Errorf("non-negative estimate violated for %d IPs", negCount)
	}

	// 2. For every unique IP, scaled estimate must not exceed f(x) + ε·N
	//    (CountMin never overestimates beyond the error bound).
	epsilon := math.E / float64(nitroCols)
	errorBound := epsilon * float64(N)
	upperViolations := 0
	for ip, freq := range truth {
		scaledEst := batch.EstimateMedian(ipKey(ip)) * float64(nitroRows)
		if scaledEst > float64(freq)+errorBound {
			upperViolations++
		}
	}

	entries := sortedByFreq(truth)
	topK := 30
	if len(entries) < topK {
		topK = len(entries)
	}

	fmt.Printf("\n=== Correctness: Top-%d Heavy Hitters (N=%d) ===\n", topK, N)
	fmt.Printf("  Rows=%d  Cols=%d  ε=e/%d=%.6f  ε·N=%.1f\n",
		nitroRows, nitroCols, nitroCols, epsilon, errorBound)
	fmt.Printf("  Note: est = raw EstimateMedian (~f(x)/rows); scaled = est × rows\n\n")
	fmt.Printf("  %-16s  %10s  %10s  %13s  %8s\n",
		"src_ip(uint32)", "truth", "est(raw)", "est(scaled)", "≤f+ε·N")

	for _, e := range entries[:topK] {
		est := batch.EstimateMedian(ipKey(e.ip))
		scaled := est * float64(nitroRows)
		within := scaled <= float64(e.count)+errorBound
		mark := "✓"
		if !within {
			mark = "✗"
		}
		fmt.Printf("  %-16d  %10d  %10.1f  %13.1f  %8s\n",
			e.ip, e.count, est, scaled, mark)
	}
	fmt.Printf("\n  Upper-bound violations (scaled > f+ε·N): %d / %d unique IPs\n\n",
		upperViolations, len(truth))

	if upperViolations > 0 {
		t.Errorf("CountMin upper-bound violated for %d IPs", upperViolations)
	}
}

// =====================================================
// Accuracy test
// =====================================================

// TestNitroCAIDAAccuracy measures estimation accuracy across all unique source IPs.
// Reports:
//   - MAE: mean absolute error over all unique IPs (scaled estimate vs truth).
//   - WRE: weighted relative error = Σ|err(x)| / N, weighting each IP's relative
//     error by its frequency share so heavy hitters dominate the metric.
//   - MRE₁₀₀: mean relative error restricted to IPs with true frequency ≥ 100,
//     filtering noise from singletons that inflate the unweighted MRE.
//   - within ε·N: fraction of IPs whose scaled estimate is within the theoretical
//     CountMin error bound.
func TestNitroCAIDAAccuracy(t *testing.T) {
	inputs, truth := loadCAIDAInputs(t)
	N := len(inputs)

	cmBatch, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	csBatch, err := NewCountSketchBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	cmBatch.Insert(inputs)
	csBatch.Insert(inputs)

	epsilon := math.E / float64(nitroCols)
	errorBound := epsilon * float64(N)

	type stats struct {
		sumAbs      float64 // Σ|err(x)| over all unique IPs — used for MAE and WRE
		sumRel      float64 // Σ(|err(x)|/f(x)) for f(x) ≥ freqThreshold — used for MRE₁₀₀
		relCount    int     // |{x : f(x) ≥ freqThreshold}|
		withinBound int     // |{x : |scaled_est - f(x)| ≤ ε·N}|
	}
	var cm, cs stats
	const freqThreshold = uint64(100)

	for ip, freq := range truth {
		key := ipKey(ip)
		f := float64(freq)

		cmScaled := cmBatch.EstimateMedian(key) * float64(nitroRows)
		csScaled := csBatch.EstimateMedian(key) * float64(nitroRows)

		cmAbs := math.Abs(cmScaled - f)
		csAbs := math.Abs(csScaled - f)

		cm.sumAbs += cmAbs
		cs.sumAbs += csAbs

		if freq >= freqThreshold {
			cm.sumRel += cmAbs / f
			cs.sumRel += csAbs / f
			cm.relCount++
			cs.relCount++
		}
		if cmAbs <= errorBound {
			cm.withinBound++
		}
		if csAbs <= errorBound {
			cs.withinBound++
		}
	}

	total := len(truth)

	// MAE: mean absolute error per unique IP.
	cmMAE := cm.sumAbs / float64(total)
	csMAE := cs.sumAbs / float64(total)

	// WRE: frequency-weighted relative error = Σ|err(x)| / N.
	// Equivalent to Σ_x [f(x)/N · |err(x)|/f(x)], so each IP's relative error
	// is weighted by its share of total traffic.
	cmWRE := cm.sumAbs / float64(N)
	csWRE := cs.sumAbs / float64(N)

	// MRE₁₀₀: mean relative error over IPs with f(x) ≥ 100.
	cmMRE100 := cm.sumRel / float64(cm.relCount)
	csMRE100 := cs.sumRel / float64(cs.relCount)

	fmt.Printf("\n=== Accuracy Report (N=%d, unique IPs=%d) ===\n", N, total)
	fmt.Printf("  Rows=%d  Cols=%d  ε=%.6f  ε·N=%.1f\n\n", nitroRows, nitroCols, epsilon, errorBound)
	fmt.Printf("  %-12s  %10s  %10s  %14s  %17s\n",
		"sketch", "MAE", "WRE", "MRE(f≥100)", "within ε·N")
	fmt.Printf("  %-12s  %10.2f  %9.4f%%  %13.4f%%  %d/%d (%.2f%%)\n",
		"CountMin",
		cmMAE, cmWRE*100, cmMRE100*100,
		cm.withinBound, total, float64(cm.withinBound)/float64(total)*100)
	fmt.Printf("  %-12s  %10.2f  %9.4f%%  %13.4f%%  %d/%d (%.2f%%)\n",
		"CountSketch",
		csMAE, csWRE*100, csMRE100*100,
		cs.withinBound, total, float64(cs.withinBound)/float64(total)*100)
	fmt.Printf("\n  WRE = Σ|err(x)| / N  (frequency-weighted; heavy hitters dominate)\n")
	fmt.Printf("  MRE(f≥100) = mean |err(x)|/f(x) for %d/%d IPs with f(x)≥100\n\n",
		cm.relCount, total)
}

// =====================================================
// Error bound test
// =====================================================

// TestNitroCAIDAErrorBound verifies the theoretical CountMin error bound:
//
//	P[ est(x) > f(x) + ε·N ] ≤ δ     where ε = e/cols, δ = e^(-rows)
//
// Since the implementation uses position%rows row assignment, the raw estimate
// is ≈ f(x)/rows. The scaled estimate (est × rows) is what satisfies the
// standard CountMin bound, so both are checked and reported.
func TestNitroCAIDAErrorBound(t *testing.T) {
	inputs, truth := loadCAIDAInputs(t)
	N := len(inputs)

	epsilon := math.E / float64(nitroCols)
	delta := math.Exp(-float64(nitroRows))
	errorBound := epsilon * float64(N)
	expectedPassFrac := 1.0 - delta

	batch, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	batch.Insert(inputs)

	var upperViolations, withinBound int
	total := len(truth)

	for ip, freq := range truth {
		scaledEst := batch.EstimateMedian(ipKey(ip)) * float64(nitroRows)
		f := float64(freq)
		// CountMin should never produce scaledEst > f(x) + ε·N
		if scaledEst > f+errorBound {
			upperViolations++
		}
		if math.Abs(scaledEst-f) <= errorBound {
			withinBound++
		}
	}

	passFrac := float64(withinBound) / float64(total)

	fmt.Printf("\n=== Error Bound Report ===\n")
	fmt.Printf("  N=%d  unique IPs=%d\n", N, total)
	fmt.Printf("  ε = e/cols = e/%d = %.6f\n", nitroCols, epsilon)
	fmt.Printf("  δ = e^(-rows) = e^(-%d) = %.6f\n", nitroRows, delta)
	fmt.Printf("  error bound ε·N = %.2f\n\n", errorBound)
	fmt.Printf("  Upper-bound violations  (scaled > f(x)+ε·N) : %d / %d\n",
		upperViolations, total)
	fmt.Printf("  Within ε·N              |scaled - f(x)| ≤ ε·N : %d / %d (%.4f%%)\n",
		withinBound, total, passFrac*100)
	fmt.Printf("  Expected pass fraction  (theory ≥ 1-δ)        : %.4f%%\n\n",
		expectedPassFrac*100)

	if upperViolations > 0 {
		t.Errorf("CountMin upper-bound violated for %d IPs (scaled_est > f(x)+ε·N)", upperViolations)
	}
	if passFrac < expectedPassFrac {
		t.Errorf("error bound pass rate %.4f%% below theoretical floor %.4f%%",
			passFrac*100, expectedPassFrac*100)
	}
}

// =====================================================
// Merge test
// =====================================================

// TestNitroCAIDAMerge splits the CAIDA stream in two halves, inserts each half into a
// separate CountMin batch, and merges them. Compares merged estimates against:
//   - a reference batch that saw the full stream
//   - the ground-truth frequencies
//
// Prints a per-IP table for the top heavy hitters and summary statistics.
//
// Note: because row assignment is positional (position % rows), the merged sketch
// and the reference batch assign different rows to the same packets, so their
// estimates will differ. The test quantifies this divergence.
func TestNitroCAIDAMerge(t *testing.T) {
	inputs, truth := loadCAIDAInputs(t)
	N := len(inputs)
	half := N / 2

	// Reference: full stream in one batch.
	ref, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	ref.Insert(inputs)

	// Split batches.
	left, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewCountMinBatch(1.0, nitroRows, nitroCols)
	if err != nil {
		t.Fatal(err)
	}
	left.Insert(inputs[:half])
	right.Insert(inputs[half:])

	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}

	epsilon := math.E / float64(nitroCols)
	errorBound := epsilon * float64(N)

	entries := sortedByFreq(truth)
	topK := 30
	if len(entries) < topK {
		topK = len(entries)
	}

	fmt.Printf("\n=== Merge Test (N=%d, split at %d) ===\n", N, half)
	fmt.Printf("  Rows=%d  Cols=%d  ε·N=%.1f\n\n", nitroRows, nitroCols, errorBound)
	fmt.Printf("  %-16s  %10s  %13s  %14s  %10s\n",
		"src_ip(uint32)", "truth", "ref(scaled)", "merged(scaled)", "|Δ|")

	var totalDiff, totalRefErr, totalMergedErr float64
	for _, e := range entries[:topK] {
		key := ipKey(e.ip)
		refScaled := ref.EstimateMedian(key) * float64(nitroRows)
		mergedScaled := left.EstimateMedian(key) * float64(nitroRows)
		diff := math.Abs(mergedScaled - refScaled)
		totalDiff += diff
		totalRefErr += math.Abs(refScaled - float64(e.count))
		totalMergedErr += math.Abs(mergedScaled - float64(e.count))
		fmt.Printf("  %-16d  %10d  %13.1f  %14.1f  %10.1f\n",
			e.ip, e.count, refScaled, mergedScaled, diff)
	}

	meanDiff := totalDiff / float64(topK)
	meanRefErr := totalRefErr / float64(topK)
	meanMergedErr := totalMergedErr / float64(topK)

	fmt.Printf("\n  Summary over top-%d heavy hitters:\n", topK)
	fmt.Printf("    Mean |merged - ref|           : %.2f\n", meanDiff)
	fmt.Printf("    Mean |ref - truth|   (error)  : %.2f\n", meanRefErr)
	fmt.Printf("    Mean |merged - truth| (error)  : %.2f\n\n", meanMergedErr)
}
