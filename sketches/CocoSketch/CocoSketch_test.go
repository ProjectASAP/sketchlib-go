package cocosketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

func TestNewCocoSketchValidation(t *testing.T) {
	if _, err := NewCocoSketch(0, 10); err == nil {
		t.Fatal("expected error when d=0")
	}
	if _, err := NewCocoSketch(5, 0); err == nil {
		t.Fatal("expected error when width=0")
	}

	cs, err := NewCocoSketch(4, 32)
	if err != nil {
		t.Fatalf("new coco failed: %v", err)
	}
	if cs.TypeName() != "CocoSketch" {
		t.Fatalf("unexpected type: %s", cs.TypeName())
	}
}

func TestCocoInsertThenEstimatePartial(t *testing.T) {
	cs, _ := NewCocoSketch(4, 64)
	cs.Insert("user:1234", 3)
	cs.Insert("user:1234", 2)

	if got := cs.Estimate("user"); got != 5 {
		t.Fatalf("expected estimate(user)=5, got %d", got)
	}
}

func TestCocoEstimateWithUDF(t *testing.T) {
	cs, _ := NewCocoSketch(4, 64)
	cs.Insert("region=us|id=1", 4)
	cs.Insert("region=eu|id=2", 6)

	matcher := func(full, partial string) bool {
		return strings.Contains(full, partial)
	}

	if got := cs.EstimateWithUDF("us", matcher); got != 4 {
		t.Fatalf("expected us total=4, got %d", got)
	}
	if got := cs.EstimateWithUDF("region", matcher); got != 10 {
		t.Fatalf("expected region total=10, got %d", got)
	}
}

func TestCocoMergeReplaysBuckets(t *testing.T) {
	left, _ := NewCocoSketch(4, 64)
	right, _ := NewCocoSketch(4, 64)

	left.Insert("alpha:key", 7)
	right.Insert("beta:key", 11)

	if err := left.Merge(right); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if got := left.Estimate("alpha"); got != 7 {
		t.Fatalf("expected alpha=7, got %d", got)
	}
	if got := left.Estimate("beta"); got != 11 {
		t.Fatalf("expected beta=11, got %d", got)
	}
}

func TestCocoHashAdapterRoundTrip(t *testing.T) {
	cs, _ := NewCocoSketch(4, 128)
	h := uint64(0x1234abcd)

	for i := 0; i < 9; i++ {
		cs.InsertWithHash(h)
	}

	got, err := cs.QueryWithHash(common.QueryFrequency, h)
	if err != nil {
		t.Fatalf("query with hash failed: %v", err)
	}
	if got < 9 {
		t.Fatalf("expected estimate >= 9, got %.0f", got)
	}
}

func TestCocoSerializeRoundTrip(t *testing.T) {
	cs, _ := NewCocoSketch(4, 128)
	cs.Insert("topic:alpha", 13)
	cs.Insert("topic:beta", 8)

	data, err := cs.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize failed: %v", err)
	}

	restored, err := DeserializeCocoSketchFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	if got := restored.Estimate("topic:alpha"); got < 13 {
		t.Fatalf("expected restored alpha >=13, got %d", got)
	}
	if got := restored.Estimate("topic:beta"); got < 8 {
		t.Fatalf("expected restored beta >=8, got %d", got)
	}
}

func ExampleCocoSketch_Insert() {
	cs, _ := NewCocoSketch(4, 64)
	cs.Insert("user:42", 5)
	fmt.Println(cs.Estimate("user"))
	// Output: 5
}

var (
	cocoCAIDAOnce   sync.Once
	cocoCAIDAHashes []uint64
	cocoCAIDAErr    error
)

func loadCAIDACocoHashes(t *testing.T) []uint64 {
	t.Helper()
	cocoCAIDAOnce.Do(func() {
		file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file1, "")
		if err != nil {
			cocoCAIDAErr = err
			return
		}

		hashes := make([]uint64, len(samples))
		var ip [4]byte
		for i, s := range samples {
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			hashes[i] = common.Hash64(ip[:])
		}
		cocoCAIDAHashes = hashes
	})

	if cocoCAIDAErr != nil {
		t.Skipf("Skipping CAIDA test: %v", cocoCAIDAErr)
	}
	if len(cocoCAIDAHashes) == 0 {
		t.Skip("No CAIDA samples found.")
	}
	return cocoCAIDAHashes
}

func topHashes(truth map[uint64]uint64, n int) []uint64 {
	type kv struct {
		k uint64
		v uint64
	}
	arr := make([]kv, 0, len(truth))
	for k, v := range truth {
		arr = append(arr, kv{k: k, v: v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
	if len(arr) < n {
		n = len(arr)
	}
	out := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, arr[i].k)
	}
	return out
}

func TestCoco_RealisticAccuracyDistribution(t *testing.T) {

	cs, _ := NewCocoSketch(5, 2048)

	hashes := loadCAIDACocoHashes(t)

	truth := map[uint64]uint64{}

	for _, h := range hashes {
		cs.InsertWithHash(h)
		truth[h]++
	}

	var totalErr float64
	sample := 5000

	keys := make([]uint64, 0, len(truth))
	for k := range truth {
		keys = append(keys, k)
	}

	rng := rand.New(rand.NewSource(99))

	for i := 0; i < sample; i++ {

		h := keys[rng.Intn(len(keys))]
		real := float64(truth[h])

		est, _ := cs.QueryWithHash(common.QueryFrequency, h)

		err := math.Abs(est-real) / math.Max(real, 1)

		totalErr += err
	}

	are := totalErr / float64(sample)

	t.Log("==== CocoSketch Realistic Accuracy ====")
	t.Logf("Stream size: %d", len(hashes))
	t.Logf("Unique keys: %d", len(truth))
	t.Logf("Average Relative Error: %.4f", are)

}

func TestCoco_HeavyHitterThreshold(t *testing.T) {

	streamSize := 100000
	thresholdRatio := 0.001

	cs, _ := NewCocoSketch(5, 2048)

	hashes := loadCAIDACocoHashes(t)[:streamSize]

	truth := map[uint64]uint64{}

	for _, h := range hashes {
		cs.InsertWithHash(h)
		truth[h]++
	}

	threshold := uint64(float64(streamSize) * thresholdRatio)

	trueHH := map[uint64]bool{}

	for k, v := range truth {
		if v >= threshold {
			trueHH[k] = true
		}
	}

	predHH := map[uint64]bool{}

	for k := range truth {
		est, _ := cs.QueryWithHash(common.QueryFrequency, k)
		if uint64(est) >= threshold {
			predHH[k] = true
		}
	}

	tp := 0
	fp := 0
	fn := 0

	for k := range predHH {
		if trueHH[k] {
			tp++
		} else {
			fp++
		}
	}

	for k := range trueHH {
		if !predHH[k] {
			fn++
		}
	}

	precision := float64(tp) / float64(tp+fp+1)
	recall := float64(tp) / float64(tp+fn+1)

	f1 := 2 * precision * recall / (precision + recall + 1e-9)

	t.Log("==== Heavy Hitter Detection ====")
	t.Logf("Precision: %.3f", precision)
	t.Logf("Recall: %.3f", recall)
	t.Logf("F1 Score: %.3f", f1)

	if recall < 0.7 {
		t.Fatalf("Recall too low: %.3f", recall)
	}
}

func TestCoco_MemoryAccuracyCurve(t *testing.T) {

	widths := []int{64, 128, 256, 512, 1024, 2048}

	hashes := loadCAIDACocoHashes(t)[:100000]

	truth := map[uint64]uint64{}

	for _, h := range hashes {
		truth[h]++
	}

	for _, w := range widths {

		cs, _ := NewCocoSketch(4, w)

		for _, h := range hashes {
			cs.InsertWithHash(h)
		}

		var totalErr float64
		count := 0

		for k, v := range truth {

			est, _ := cs.QueryWithHash(common.QueryFrequency, k)

			err := math.Abs(est-float64(v)) / math.Max(float64(v), 1)

			totalErr += err
			count++

			if count > 2000 {
				break
			}
		}

		are := totalErr / float64(count)

		t.Logf("Width=%d ARE=%.4f", w, are)
	}
}

func TestCoco_UniformTraffic(t *testing.T) {

	cs, _ := NewCocoSketch(5, 2048)

	rng := rand.New(rand.NewSource(99))

	truth := map[string]uint64{}

	for i := 0; i < 100000; i++ {

		k := fmt.Sprintf("flow-%d", rng.Intn(20000))

		truth[k]++
		cs.Insert(k, 1)
	}

	var totalErr float64
	count := 0

	for k, v := range truth {

		est := float64(cs.Estimate(k))
		real := float64(v)

		err := math.Abs(est-real) / math.Max(real, 1)

		totalErr += err
		count++

		if count > 2000 {
			break
		}
	}

	are := totalErr / float64(count)

	t.Log("==== Uniform Traffic Accuracy ====")
	t.Logf("ARE: %.4f", are)
}
