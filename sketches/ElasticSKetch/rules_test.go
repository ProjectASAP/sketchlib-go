package elasticsketch

import (
	"slices"
	"strconv"
	"testing"
)

// keyAt returns a key, other than the excluded ones, whose bucket is idx.
func keyAt(t *testing.T, es *ElasticSketch, idx int, exclude ...string) string {
	t.Helper()
	for i := 0; i < 1_000_000; i++ {
		k := "key:" + strconv.Itoa(i)
		if es.bucketIndex(k) == idx && !slices.Contains(exclude, k) {
			return k
		}
	}
	t.Fatalf("no key for bucket %d", idx)
	return ""
}

// addAt adds v to the light cells id hashes to in m, one per row.
func addAt(es *ElasticSketch, m [][]int32, id string, v int32) {
	for r := range m {
		m[r][es.lightCol(r, id)] += v
	}
}

func zeroLight(es *ElasticSketch) [][]int32 {
	m := make([][]int32, es.light.Rows())
	for r := range m {
		m[r] = make([]int32, es.light.Cols())
	}
	return m
}

func checkLight(t *testing.T, es *ElasticSketch, want [][]int32) {
	t.Helper()
	for r := range want {
		if got := es.light.RowSlice(r); !slices.Equal(got, want[r]) {
			t.Fatalf("light row %d = %v, want %v", r, got, want[r])
		}
	}
}

func checkBucket(t *testing.T, es *ElasticSketch, idx int, want HeavyBucket) {
	t.Helper()
	if got := es.heavy[idx]; got != want {
		t.Fatalf("bucket %d = %+v, want %+v", idx, got, want)
	}
}

func TestElastic_TakeoverAtLambdaNegativeVotes(t *testing.T) {
	es := newSketch(t, 8, 2, 16)
	resident := "key:resident"
	idx := es.bucketIndex(resident)
	arrival := keyAt(t, es, idx, resident)
	light := zeroLight(es)

	es.Insert(resident)
	for i := int32(1); i < Lambda; i++ {
		es.Insert(arrival)
		addAt(es, light, arrival, 1)
		checkBucket(t, es, idx, HeavyBucket{FlowID: resident, VotePos: 1, VoteNeg: i})
		checkLight(t, es, light)
	}

	es.Insert(arrival)
	addAt(es, light, resident, 1)
	checkBucket(t, es, idx, HeavyBucket{FlowID: arrival, VotePos: 1, VoteNeg: 1, Eviction: true})
	checkLight(t, es, light)
}

func TestElastic_ArrivalSeatsOverStaleCopy(t *testing.T) {
	es := newSketch(t, 2, 2, 16)
	resident := "key:resident"
	es.InsertN(resident, 5)
	if err := es.ExpandHeavy(); err != nil {
		t.Fatal(err)
	}
	live := es.bucketIndex(resident)
	stale := live ^ 2
	checkBucket(t, es, stale, HeavyBucket{FlowID: resident, VotePos: 5})

	arrival := keyAt(t, es, stale, resident)
	es.Insert(arrival)
	checkBucket(t, es, stale, HeavyBucket{FlowID: arrival, VotePos: 1, Eviction: true})
	checkBucket(t, es, live, HeavyBucket{FlowID: resident, VotePos: 5})
	checkLight(t, es, zeroLight(es))
	if !es.staleCopies {
		t.Fatal("staleCopies cleared by a seat")
	}
	want := []FlowCount{{arrival, 1}, {resident, 5}}
	if arrival > resident {
		want[0], want[1] = want[1], want[0]
	}
	if got := es.HeavyHitters(1); !slices.Equal(got, want) {
		t.Fatalf("heavy hitters %v, want %v", got, want)
	}
}

func TestElastic_MergeMaxOfDisjointSketches(t *testing.T) {
	a, b := newSketch(t, 8, 2, 4), newSketch(t, 8, 2, 4)
	fa := "key:a"
	fb := keyAt(t, a, (a.bucketIndex(fa)+1)%8)
	a.InsertN(fa, 4)
	b.InsertN(fb, 3)
	aLight := [][]int32{{1, 5, 0, 7}, {2, 0, 9, 3}}
	bLight := [][]int32{{4, 2, 6, 7}, {0, 8, 1, 3}}
	for r := range aLight {
		copy(a.light.RowSlice(r), aLight[r])
		copy(b.light.RowSlice(r), bLight[r])
	}

	if err := a.MergeMax(b); err != nil {
		t.Fatal(err)
	}
	checkLight(t, a, [][]int32{{4, 5, 6, 7}, {2, 8, 9, 3}})
	for idx := range a.heavy {
		switch idx {
		case a.bucketIndex(fa):
			checkBucket(t, a, idx, HeavyBucket{FlowID: fa, VotePos: 4, Eviction: true})
		case a.bucketIndex(fb):
			checkBucket(t, a, idx, HeavyBucket{FlowID: fb, VotePos: 3, Eviction: true})
		default:
			checkBucket(t, a, idx, HeavyBucket{Eviction: true})
		}
	}
}

func TestElastic_FullBucketCount(t *testing.T) {
	es := newSketch(t, 8, 2, 16)
	for i, votes := range []int32{1, 5, 10} {
		es.InsertN(keyAt(t, es, i), votes)
	}
	for t2, want := range map[int32]int{0: 3, 1: 2, 4: 2, 5: 1, 9: 1, 10: 0} {
		if got := es.FullBucketCount(t2); got != want {
			t.Errorf("FullBucketCount(%d) = %d, want %d", t2, got, want)
		}
	}
}

func TestElastic_WeightedTakeover(t *testing.T) {
	es := newSketch(t, 8, 2, 16)
	resident := "key:resident"
	idx := es.bucketIndex(resident)
	arrival := keyAt(t, es, idx, resident)
	light := zeroLight(es)

	es.InsertN(resident, 2)
	es.InsertN(arrival, 3)
	addAt(es, light, arrival, 3)
	checkBucket(t, es, idx, HeavyBucket{FlowID: resident, VotePos: 2, VoteNeg: 3})
	checkLight(t, es, light)

	es.InsertN(arrival, 2*Lambda-3)
	addAt(es, light, resident, 2)
	checkBucket(t, es, idx, HeavyBucket{FlowID: arrival, VotePos: 2*Lambda - 3, VoteNeg: 2*Lambda - 3, Eviction: true})
	checkLight(t, es, light)
}
