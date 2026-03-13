package elasticsketch

import (
	"math"
	"strconv"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
)

func mustNewElasticForTest(t *testing.T, buckets int) *ElasticSketch {
	t.Helper()
	es, err := New(Config{BucketCount: buckets})
	if err != nil {
		t.Fatalf("new elastic: %v", err)
	}
	return es
}

func TestElasticHeavyBucketTracksRepeatedFlow(t *testing.T) {
	es := mustNewElasticForTest(t, 8)
	flow := "flow::primary"

	for i := 0; i < 12; i++ {
		es.Insert(flow)
	}

	if got := es.Query(flow); got != 12 {
		t.Fatalf("expected exact heavy count 12, got %d", got)
	}
	if got := es.Query("other"); got != 0 {
		t.Fatalf("expected other count 0, got %d", got)
	}
	if len(es.heavy) != 8 {
		t.Fatalf("expected 8 heavy buckets, got %d", len(es.heavy))
	}
	if es.light.Rows() != elasticLightRows || es.light.Cols() != elasticLightCols {
		t.Fatalf("unexpected light layout: got %dx%d", es.light.Rows(), es.light.Cols())
	}
}

func TestElasticLightSketchCountsCollidingFlows(t *testing.T) {
	es := mustNewElasticForTest(t, 8)
	primary := "flow::primary"
	primaryIdx := int(hashForElastic(primary) % uint64(es.bktlen))

	secondary := ""
	for i := 0; i < 10000; i++ {
		cand := makeCollisionCandidate(i)
		if int(hashForElastic(cand)%uint64(es.bktlen)) == primaryIdx && cand != primary {
			secondary = cand
			break
		}
	}
	if secondary == "" {
		t.Fatal("unable to find colliding key")
	}

	for i := 0; i < 10; i++ {
		es.Insert(primary)
	}
	for i := 0; i < 6; i++ {
		es.Insert(secondary)
	}

	heavyEst := es.Query(primary)
	lightEst := es.Query(secondary)

	if heavyEst < 10 {
		t.Fatalf("expected heavy estimate >=10, got %d", heavyEst)
	}
	if lightEst < 6 {
		t.Fatalf("expected light estimate >=6, got %d", lightEst)
	}
	if es.lightEstimateHash(hashForElastic(secondary)) < 6 {
		t.Fatalf("expected direct light estimate >=6, got %.0f", es.lightEstimateHash(hashForElastic(secondary)))
	}
}

func TestElasticSerializeRoundTrip(t *testing.T) {
	es := mustNewElasticForTest(t, 4)
	es.InsertN("foo", 7)
	es.InsertN("bar", 3)

	blob, err := es.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize failed: %v", err)
	}

	restored, err := DeserializeElasticSketchFromBytes(blob)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	if got := restored.Query("foo"); got < 7 {
		t.Fatalf("expected restored foo >=7, got %d", got)
	}
	if got := restored.Query("bar"); got < 3 {
		t.Fatalf("expected restored bar >=3, got %d", got)
	}
	if len(restored.heavy) != len(es.heavy) {
		t.Fatalf("heavy bucket length mismatch after restore: got %d want %d", len(restored.heavy), len(es.heavy))
	}
	if restored.light.Rows() != es.light.Rows() || restored.light.Cols() != es.light.Cols() {
		t.Fatalf("light layout mismatch after restore: got %dx%d want %dx%d", restored.light.Rows(), restored.light.Cols(), es.light.Rows(), es.light.Cols())
	}
}

func TestElastic_RustAlignedLayout(t *testing.T) {
	es := mustNewElasticForTest(t, 16)

	if len(es.heavy) != 16 {
		t.Fatalf("expected heavy Vec-style layout of 16 buckets, got %d", len(es.heavy))
	}
	for i, bucket := range es.heavy {
		if bucket.FlowID != "" || bucket.VotePos != 0 || bucket.VoteNeg != 0 || bucket.Eviction {
			t.Fatalf("heavy bucket %d not zero-initialized: %+v", i, bucket)
		}
	}

	if es.light == nil {
		t.Fatal("light part is nil")
	}
	if es.light.Rows() != elasticLightRows || es.light.Cols() != elasticLightCols {
		t.Fatalf("light part should be Vector2D[%d x %d], got %dx%d", elasticLightRows, elasticLightCols, es.light.Rows(), es.light.Cols())
	}
}

func hashForElastic(key string) uint64 {
	return commonHashCanonical([]byte(key))
}

func makeCollisionCandidate(i int) string {
	return "flow::secondary::" + strconv.Itoa(i)
}

// small helpers to avoid extra deps in tests
func commonHashCanonical(b []byte) uint64 {
	return common.HashIt(common.CanonicalHashSeed, b)
}

func mustElasticQuality(t *testing.T, buckets int) *ElasticSketch {
	t.Helper()
	es, err := New(Config{BucketCount: buckets})
	if err != nil {
		t.Fatalf("new elastic: %v", err)
	}
	return es
}

func findElasticCollision(bucketCount int, target string) string {
	targetIdx := int(common.HashIt(common.CanonicalHashSeed, []byte(target)) % uint64(bucketCount))
	for i := 0; i < 1_000_000; i++ {
		cand := "elastic-collision-" + strconv.Itoa(i)
		if cand == target {
			continue
		}
		idx := int(common.HashIt(common.CanonicalHashSeed, []byte(cand)) % uint64(bucketCount))
		if idx == targetIdx {
			return cand
		}
	}
	return ""
}

func TestElastic_Quality_OperationsAndErrorBound(t *testing.T) {
	es := mustElasticQuality(t, 64)
	main := "flow-main"
	collider := findElasticCollision(es.bktlen, main)
	if collider == "" {
		t.Fatal("failed to find collider")
	}

	for i := 0; i < 200; i++ {
		es.Insert(main)
	}
	for i := 0; i < 30; i++ {
		es.Insert(collider)
	}

	gotMain := es.Query(main)
	gotColl := es.Query(collider)
	if gotMain < 200 {
		t.Fatalf("main underestimated: got=%d want>=200", gotMain)
	}
	if gotColl < 30 {
		t.Fatalf("collider underestimated: got=%d want>=30", gotColl)
	}

	// Conservative upper bound from Count-Min light layer epsilon (w=4096).
	epsBound := int(math.Ceil((2.718281828 / 4096.0) * 230.0))
	if gotColl > 30+epsBound+20 {
		t.Fatalf("collider overestimate too high: got=%d bound=%d", gotColl, 30+epsBound+20)
	}
}

func TestElastic_Quality_SerializeRoundTrip(t *testing.T) {
	es := mustElasticQuality(t, 128)
	for i := 0; i < 100; i++ {
		es.Insert("k:" + strconv.Itoa(i%7))
	}
	b, err := es.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	r, err := DeserializeElasticSketchFromBytes(b)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	for i := 0; i < 7; i++ {
		k := "k:" + strconv.Itoa(i)
		if r.Query(k) != es.Query(k) {
			t.Fatalf("roundtrip mismatch key=%s got=%d want=%d", k, r.Query(k), es.Query(k))
		}
	}
}

func TestElastic_Merge_DisjointHeavyBuckets(t *testing.T) {
	left := mustElasticQuality(t, 64)
	right := mustElasticQuality(t, 64)

	left.InsertN("flow:left", 40)
	right.InsertN("flow:right", 25)

	if err := left.Merge(right); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if got := left.Query("flow:left"); got < 40 {
		t.Fatalf("left flow underestimated after merge: got=%d want>=40", got)
	}
	if got := left.Query("flow:right"); got < 25 {
		t.Fatalf("right flow underestimated after merge: got=%d want>=25", got)
	}
}

func TestElastic_Merge_CollidingFlows(t *testing.T) {
	left := mustElasticQuality(t, 16)
	main := "flow-main"
	collider := findElasticCollision(left.bktlen, main)
	if collider == "" {
		t.Fatal("failed to find collider")
	}

	right := mustElasticQuality(t, 16)
	for i := 0; i < 30; i++ {
		left.Insert(main)
	}
	for i := 0; i < 12; i++ {
		right.Insert(collider)
	}

	beforeMain := left.Query(main)
	beforeCollider := right.Query(collider)

	if err := left.Merge(right); err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	afterMain := left.Query(main)
	afterCollider := left.Query(collider)
	if afterMain < beforeMain {
		t.Fatalf("main flow regressed after merge: before=%d after=%d", beforeMain, afterMain)
	}
	if afterCollider < beforeCollider {
		t.Fatalf("collider regressed after merge: before=%d after=%d", beforeCollider, afterCollider)
	}
}
