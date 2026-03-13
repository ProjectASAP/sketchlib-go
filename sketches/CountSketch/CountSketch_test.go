package countsketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// ==============================================================================
// Helper to load CAIDA dataset
// ==============================================================================

func loadCAIDA(t *testing.T) []testdata.Sample {

	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}

	if len(samples) == 0 {
		t.Skip("No CAIDA samples found.")
	}

	t.Logf("Loaded CAIDA dataset: %d packets", len(samples))

	return samples
}

//////////////////////////////////////////////////////////////////
// 1. BASIC PROPERTY TESTS
//////////////////////////////////////////////////////////////////

func TestCS_ZeroState(t *testing.T) {

	cs, _ := NewCountSketch(5, 1024)

	est := float64(cs.EstimateStringCount("never_seen"))

	t.Logf("Zero state estimate: %.2f", est)

	if math.Abs(est) > 0 {
		t.Fatalf("zero-state incorrect")
	}
}

func TestCS_SingleKeyCorrectness(t *testing.T) {

	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 1000; i++ {
		cs.UpdateString("key", 1)
	}

	est := float64(cs.EstimateStringCount("key"))

	t.Logf("Inserted 1000 updates, estimate=%.2f", est)

	if math.Abs(est-1000) > 2 {
		t.Fatalf("single-key incorrect")
	}
}

func TestCS_MedianEstimator(t *testing.T) {

	cs, _ := NewCountSketch(3, 1024)

	hash := common.Hash64([]byte("k"))

	c, sign := cs.derivePosAndSign(hash, 0)

	cs.Count[0][c] += 10000 * float64(sign)

	for i := 0; i < 3; i++ {
		cs.InsertWithHash(hash)
	}

	est, _ := cs.QueryWithHash(common.QueryFrequency, hash)

	t.Logf("Median estimate after poisoning row: %.2f", est)

	if est < 2 || est > 4 {
		t.Fatalf("median estimator broken: got %.2f", est)
	}
}

func TestCS_SignCorrectness(t *testing.T) {

	cs, _ := NewCountSketch(5, 1024)

	for i := 0; i < 100; i++ {
		cs.UpdateString("pos", 1)
		cs.UpdateString("neg", -1)
	}

	estPos := float64(cs.EstimateStringCount("pos"))
	estNeg := float64(cs.EstimateStringCount("neg"))

	t.Logf("Estimate pos=%.2f neg=%.2f", estPos, estNeg)

	if estPos <= 0 || estNeg >= 0 {
		t.Fatalf("sign incorrect")
	}
}

//////////////////////////////////////////////////////////////////
// 2. CAIDA DATASET TESTS
//////////////////////////////////////////////////////////////////

func TestCS_CAIDA_MergeCorrectness(t *testing.T) {

	samples := loadCAIDA(t)

	mid := len(samples) / 2

	rows, cols := 5, 2048

	csPart1, _ := NewCountSketch(rows, cols)
	csPart2, _ := NewCountSketch(rows, cols)

	csTotal, _ := NewCountSketch(rows, cols)

	for i, s := range samples {

		ipUint := uint32(s.F)

		ipBytes := make([]byte, 4)

		binary.BigEndian.PutUint32(ipBytes, ipUint)

		h := common.Hash64(ipBytes)

		csTotal.InsertWithHash(h)

		if i < mid {
			csPart1.InsertWithHash(h)
		} else {
			csPart2.InsertWithHash(h)
		}
	}

	if err := csPart1.Merge(csPart2); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	t.Log("Verifying merged matrix equality...")

	for r := 0; r < rows; r++ {

		for c := 0; c < cols; c++ {

			if csPart1.Count[r][c] != csTotal.Count[r][c] {

				t.Fatalf("Mismatch at [%d][%d]", r, c)

			}
		}
	}

	t.Logf("Merge Correctness Verified on %d packets", len(samples))
}

//////////////////////////////////////////////////////////////////
// 3. ACCURACY TEST WITH FULL REPORT
//////////////////////////////////////////////////////////////////

func TestCS_CAIDA_Accuracy(t *testing.T) {

	samples := loadCAIDA(t)

	rows, cols := 5, 2048

	cs, err := NewCountSketch(rows, cols)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	groundTruth := make(map[uint32]int64)

	for _, s := range samples {

		ip := uint32(s.F)

		groundTruth[ip]++

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ip)

		cs.InsertWithHash(common.Hash64(ipBytes))
	}

	type kv struct {
		IP    uint32
		Count int64
	}

	var sorted []kv

	for k, v := range groundTruth {
		sorted = append(sorted, kv{k, v})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Count > sorted[j].Count
	})

	topK := 100

	if len(sorted) < topK {
		topK = len(sorted)
	}

	var totalRelErr float64

	var maxErr float64

	minErr := math.MaxFloat64

	buckets := make([]int, 5)

	t.Log("Top Heavy Hitters Accuracy Report")

	for i := 0; i < topK; i++ {

		item := sorted[i]

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, item.IP)

		est, _ := cs.QueryWithHash(common.QueryFrequency, common.Hash64(ipBytes))

		err := math.Abs(est - float64(item.Count))

		relErr := err / float64(item.Count)

		totalRelErr += relErr

		if relErr > maxErr {
			maxErr = relErr
		}

		if relErr < minErr {
			minErr = relErr
		}

		switch {
		case relErr < 0.01:
			buckets[0]++
		case relErr < 0.05:
			buckets[1]++
		case relErr < 0.10:
			buckets[2]++
		case relErr < 0.20:
			buckets[3]++
		default:
			buckets[4]++
		}

		t.Logf(
			"Rank %3d | True=%8d | Est=%8.2f | RelErr=%6.3f%%",
			i+1,
			item.Count,
			est,
			relErr*100,
		)
	}

	avgRelErr := totalRelErr / float64(topK)

	t.Log("===================================================")
	t.Log(" CountSketch Accuracy Summary")
	t.Log("---------------------------------------------------")
	t.Logf("Packets processed : %d", len(samples))
	t.Logf("Unique keys       : %d", len(groundTruth))
	t.Logf("TopK evaluated    : %d", topK)
	t.Log("---------------------------------------------------")
	t.Logf("Average Error     : %.4f%%", avgRelErr*100)
	t.Logf("Max Error         : %.4f%%", maxErr*100)
	t.Logf("Min Error         : %.4f%%", minErr*100)
	t.Log("---------------------------------------------------")
	t.Log("Error Distribution")
	t.Logf("<1%%   : %d", buckets[0])
	t.Logf("1-5%%  : %d", buckets[1])
	t.Logf("5-10%% : %d", buckets[2])
	t.Logf("10-20%%: %d", buckets[3])
	t.Logf(">20%%  : %d", buckets[4])
	t.Log("===================================================")

	if avgRelErr > 0.20 {
		t.Fatalf("Accuracy too low: %.2f%%", avgRelErr*100)
	}
}

//////////////////////////////////////////////////////////////////
// 4. RANDOMIZED QUALITY TEST
//////////////////////////////////////////////////////////////////

type csUpdate struct {
	hash  uint64
	delta float64
}

func buildCSWorkload(n, keys int, seed int64) ([]csUpdate, map[uint64]float64) {

	rng := rand.New(rand.NewSource(seed))

	stream := make([]csUpdate, n)

	truth := make(map[uint64]float64)

	for i := 0; i < n; i++ {

		k := rng.Intn(keys)

		h := common.FromString(fmt.Sprintf("cs:%d", k)).Hash

		d := 1.0

		if rng.Intn(5) == 0 {
			d = -1.0
		}

		stream[i] = csUpdate{hash: h, delta: d}

		truth[h] += d
	}

	return stream, truth
}

func TestCountSketch_Quality_OperationsAndErrorBound(t *testing.T) {

	const rows, cols = 7, 4096

	stream, truth := buildCSWorkload(50000, 1500, 19)

	t.Logf("Generated workload: %d updates, %d keys", len(stream), len(truth))

	s, _ := NewCountSketch(rows, cols)

	for _, u := range stream {
		s.InsertWithHashAndValue(u.hash, u.delta)
	}

	f2 := 0.0

	for _, v := range truth {
		f2 += v * v
	}

	l2norm := math.Sqrt(f2)

	bound := 4.0 * l2norm / math.Sqrt(float64(cols))

	t.Logf("L2 norm: %.2f", l2norm)
	t.Logf("Expected error bound: %.6f", bound)

	for h, real := range truth {

		est, _ := s.QueryWithHash(common.QueryFrequency, h)

		if math.Abs(est-real) > 3.0*bound {

			t.Fatalf("error too large: est=%v real=%v", est, real)

		}
	}

	t.Log("Quality bound test passed.")
}
