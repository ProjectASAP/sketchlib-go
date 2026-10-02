package elasticsketch

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// Lambda is the eviction threshold: a resident flow is replaced once its
// negative votes reach Lambda times its positive votes.
const Lambda int32 = 8

// Light-layer dimensions used when Config leaves them zero.
const (
	DefaultLightRows = 3
	DefaultLightCols = 4096
)

// Config sizes the heavy table and the light Count-Min layer.
type Config struct {
	BucketCount int
	LightRows   int
	LightCols   int
}

// HeavyBucket is one slot of the heavy part. It holds a flow exactly while
// VotePos is non-zero; Eviction marks that part of the flow's size lives in
// the light layer.
type HeavyBucket struct {
	FlowID   string
	VotePos  int32
	VoteNeg  int32
	Eviction bool
}

func (b *HeavyBucket) vacant() bool { return b.VotePos == 0 }

func (b *HeavyBucket) clear() {
	b.FlowID = ""
	b.VotePos = 0
	b.VoteNeg = 0
}

// FlowCount is a flow and its estimated size.
type FlowCount struct {
	FlowID string
	Count  int
}

// ElasticSketch is a heavy hash table over flow ids, placed at the canonical
// seed, backed by an int32 Count-Min light layer whose row r hashes at seed
// index r and takes the lower 32 bits modulo the column count.
type ElasticSketch struct {
	heavy       []HeavyBucket
	bktlen      int
	light       *storage.Vector2D[int32]
	staleCopies bool

	mu sync.Mutex
}

// New creates an Elastic sketch with cfg.BucketCount heavy buckets over a
// cfg.LightRows x cfg.LightCols light layer.
func New(cfg Config) (*ElasticSketch, error) {
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	light, err := storage.InitVector2D[int32](cfg.LightRows, cfg.LightCols)
	if err != nil {
		return nil, err
	}
	return &ElasticSketch{
		heavy:  make([]HeavyBucket, cfg.BucketCount),
		bktlen: cfg.BucketCount,
		light:  light,
	}, nil
}

func (cfg *Config) normalize() error {
	if cfg.BucketCount <= 0 || cfg.BucketCount > math.MaxInt32 {
		return fmt.Errorf("BucketCount must be in [1, %d], got %d", math.MaxInt32, cfg.BucketCount)
	}
	if cfg.LightRows == 0 {
		cfg.LightRows = DefaultLightRows
	}
	if cfg.LightCols == 0 {
		cfg.LightCols = DefaultLightCols
	}
	if cfg.LightRows < 0 || cfg.LightCols < 0 {
		return fmt.Errorf("light layer dimensions must be positive, got %dx%d", cfg.LightRows, cfg.LightCols)
	}
	return nil
}

// Insert records one occurrence of key.
func (es *ElasticSketch) Insert(key string) {
	es.InsertN(key, 1)
}

// InsertN records count occurrences of key in one step: a matching bucket
// takes count positive votes, a non-matching one count negative votes, and a
// takeover seats the arrival with count of each. Non-positive counts are ignored.
func (es *ElasticSketch) InsertN(key string, count int32) {
	if count <= 0 {
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	es.insertLocked(key, count)
}

// InsertInput inserts one event from common.SketchInput, keyed by its bytes,
// which must be valid UTF-8 for the sketch to encode.
func (es *ElasticSketch) InsertInput(input *common.SketchInput) {
	es.InsertInputN(input, 1)
}

// InsertInputN inserts count events from common.SketchInput, keyed by its
// bytes, which must be valid UTF-8 for the sketch to encode.
func (es *ElasticSketch) InsertInputN(input *common.SketchInput, count int32) {
	if input == nil {
		return
	}
	es.InsertN(string(input.Bytes), count)
}

// InsertWithHash inserts one event keyed by the hex form of hash.
func (es *ElasticSketch) InsertWithHash(hash uint64) {
	es.InsertWithHashN(hash, 1)
}

// InsertWithHashN inserts count events keyed by the hex form of hash.
func (es *ElasticSketch) InsertWithHashN(hash uint64, count int32) {
	es.InsertN(fmt.Sprintf("%016x", hash), count)
}

func (es *ElasticSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q != common.QueryFrequency {
		return 0, common.ErrUnsupportedQuery
	}
	return float64(es.Query(fmt.Sprintf("%016x", hash))), nil
}

func (es *ElasticSketch) TypeName() string {
	return "elastic"
}

func (es *ElasticSketch) insertLocked(id string, count int32) {
	idx := es.bucketIndex(id)
	if es.staleAt(idx) {
		es.seatOverStaleCopy(idx, id, count)
		return
	}
	b := &es.heavy[idx]
	if b.vacant() {
		b.FlowID = id
		b.VotePos = count
		b.VoteNeg = 0
		return
	}
	if b.FlowID == id {
		b.VotePos += count
		return
	}

	b.VoteNeg += count
	if b.VoteNeg < Lambda*b.VotePos {
		es.lightInsert(id, count)
		return
	}

	evictedID, evictedVotes := b.FlowID, b.VotePos
	b.FlowID = id
	b.VotePos = count
	b.VoteNeg = count
	b.Eviction = true
	es.lightInsert(evictedID, evictedVotes)
}

// Query returns the estimated count for id: the resident vote count, plus the
// light layer whenever the bucket carries the eviction flag.
func (es *ElasticSketch) Query(id string) int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.queryLocked(id)
}

func (es *ElasticSketch) queryLocked(id string) int {
	b := &es.heavy[es.bucketIndex(id)]
	if !b.vacant() && b.FlowID == id {
		if b.Eviction {
			return int(b.VotePos) + int(es.lightEstimate(id))
		}
		return int(b.VotePos)
	}
	return int(es.lightEstimate(id))
}

// Merge merges other in, keeping elephants in the heavy part and adding the
// light layers counter by counter.
func (es *ElasticSketch) Merge(other common.Sketch) error {
	return es.merge(other, func(dst, src []int32) {
		for c := range dst {
			dst[c] += src[c]
		}
	})
}

// MergeMax merges other in, keeping elephants in the heavy part and the larger
// of each light counter pair. It requires the two sketches to have observed
// disjoint flow sets.
func (es *ElasticSketch) MergeMax(other *ElasticSketch) error {
	return es.merge(other, func(dst, src []int32) {
		for c := range dst {
			dst[c] = max(dst[c], src[c])
		}
	})
}

func (es *ElasticSketch) merge(other common.Sketch, mergeRow func(dst, src []int32)) error {
	o, ok := other.(*ElasticSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	if o == nil {
		return errors.New("cannot merge: nil sketch")
	}
	if es == o {
		es.mu.Lock()
		o = es.cloneLocked()
		es.mu.Unlock()
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()

	if es.bktlen != o.bktlen {
		return errors.New("cannot merge: different bucket count")
	}
	if es.light.Rows() != o.light.Rows() || es.light.Cols() != o.light.Cols() {
		return errors.New("cannot merge: different light matrix dimensions")
	}

	losers := es.contestHeavyAgainst(o)
	for r := 0; r < es.light.Rows(); r++ {
		mergeRow(es.light.RowSlice(r), o.light.RowSlice(r))
	}
	for _, l := range losers {
		es.lightInsert(l.id, l.votes)
	}
	return nil
}

type spilled struct {
	id    string
	votes int32
}

// contestHeavyAgainst combines the two heavy parts bucket by bucket: a flow
// both hold keeps its bucket with the votes summed, otherwise the larger
// estimate wins. Every bucket ends up flagged; the losers are returned.
func (es *ElasticSketch) contestHeavyAgainst(o *ElasticSketch) []spilled {
	var losers []spilled
	merged := make([]*HeavyBucket, len(es.heavy))
	for idx := range es.heavy {
		var mine, theirs *HeavyBucket
		if !es.heavy[idx].vacant() && !es.staleAt(idx) {
			b := es.heavy[idx]
			mine = &b
		}
		if !o.heavy[idx].vacant() && !o.staleAt(idx) {
			b := o.heavy[idx]
			theirs = &b
		}
		switch {
		case mine == nil:
			merged[idx] = theirs
		case theirs == nil:
			merged[idx] = mine
		case mine.FlowID == theirs.FlowID:
			mine.VotePos += theirs.VotePos
			mine.VoteNeg = max(mine.VoteNeg, theirs.VoteNeg)
			merged[idx] = mine
		default:
			kept, lost := mine, theirs
			if es.queryLocked(mine.FlowID) < o.queryLocked(theirs.FlowID) {
				kept, lost = theirs, mine
			}
			losers = append(losers, spilled{lost.FlowID, lost.VotePos})
			merged[idx] = kept
		}
	}
	for idx, kept := range merged {
		if kept != nil {
			es.heavy[idx] = *kept
		} else {
			es.heavy[idx].clear()
		}
		es.heavy[idx].Eviction = true
	}
	es.staleCopies = false
	return losers
}

// ExpandHeavy doubles the heavy table by appending a copy of itself. Every
// resident then sits in both halves; the copy in the half it does not hash to
// is stale and is dropped lazily.
func (es *ElasticSketch) ExpandHeavy() error {
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.bktlen > math.MaxInt32/2 {
		return fmt.Errorf("heavy table of %d buckets cannot double within int32", es.bktlen)
	}
	es.heavy = append(es.heavy, es.heavy...)
	es.bktlen *= 2
	es.staleCopies = true
	return nil
}

// CompressHeavy shrinks the heavy table by ratio, which must divide the
// bucket count. New bucket j absorbs old buckets j, j+w', j+2w', ...; the
// largest resident of each group keeps its bucket and the rest spill.
func (es *ElasticSketch) CompressHeavy(ratio int) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	if ratio < 1 {
		return fmt.Errorf("compression ratio must be at least 1, got %d", ratio)
	}
	if es.bktlen%ratio != 0 {
		return fmt.Errorf("compression ratio %d must divide the bucket count %d", ratio, es.bktlen)
	}
	if ratio == 1 {
		return nil
	}
	es.dropStaleCopies()

	width := es.bktlen / ratio
	winner := make([]int, width)
	best := make([]int, width)
	flagged := make([]bool, width)
	for g := range winner {
		winner[g] = -1
	}
	for idx := range es.heavy {
		g := idx % width
		if es.heavy[idx].vacant() {
			flagged[g] = flagged[g] || es.heavy[idx].Eviction
			continue
		}
		size := es.queryLocked(es.heavy[idx].FlowID)
		if winner[g] < 0 || size > best[g] {
			best[g] = size
			winner[g] = idx
		}
	}

	compressed := make([]HeavyBucket, width)
	var losers []spilled
	for idx, b := range es.heavy {
		g := idx % width
		if b.vacant() {
			continue
		}
		if winner[g] == idx {
			compressed[g] = b
		} else {
			losers = append(losers, spilled{b.FlowID, b.VotePos})
		}
	}
	for g := range compressed {
		if compressed[g].vacant() && flagged[g] {
			compressed[g].Eviction = true
		}
	}
	es.heavy = compressed
	es.bktlen = width
	for _, l := range losers {
		es.lightInsert(l.id, l.votes)
	}
	return nil
}

// HeavyHitters returns every flow in the heavy part whose estimate reaches
// threshold, sorted by flow id.
func (es *ElasticSketch) HeavyHitters(threshold int) []FlowCount {
	es.mu.Lock()
	defer es.mu.Unlock()
	var out []FlowCount
	for idx := range es.heavy {
		if es.heavy[idx].vacant() || es.staleAt(idx) {
			continue
		}
		id := es.heavy[idx].FlowID
		if size := es.queryLocked(id); size >= threshold {
			out = append(out, FlowCount{id, size})
		}
	}
	slices.SortFunc(out, func(a, b FlowCount) int { return strings.Compare(a.FlowID, b.FlowID) })
	return out
}

// FullBucketCount returns how many buckets hold a flow with more than t2
// positive votes.
func (es *ElasticSketch) FullBucketCount(t2 int32) int {
	es.mu.Lock()
	defer es.mu.Unlock()
	n := 0
	for i := range es.heavy {
		if !es.heavy[i].vacant() && es.heavy[i].VotePos > t2 {
			n++
		}
	}
	return n
}

// dropStaleCopies empties every bucket holding a stale copy, keeping its
// eviction flag set.
func (es *ElasticSketch) dropStaleCopies() {
	if !es.staleCopies {
		return
	}
	for idx := range es.heavy {
		if es.staleAt(idx) {
			es.heavy[idx].clear()
			es.heavy[idx].Eviction = true
		}
	}
	es.staleCopies = false
}

// staleAt reports whether the bucket at idx holds a copy left by an expansion
// whose resident now hashes elsewhere.
func (es *ElasticSketch) staleAt(idx int) bool {
	if !es.staleCopies {
		return false
	}
	b := &es.heavy[idx]
	return !b.vacant() && es.bucketIndex(b.FlowID) != idx
}

// seatOverStaleCopy replaces a stale copy with id, flagged.
func (es *ElasticSketch) seatOverStaleCopy(idx int, id string, count int32) {
	b := &es.heavy[idx]
	b.FlowID = id
	b.VotePos = count
	b.VoteNeg = 0
	b.Eviction = true
}

func (es *ElasticSketch) bucketIndex(id string) int {
	return int(common.HashIt(common.CanonicalHashSeed, []byte(id)) % uint64(es.bktlen))
}

func (es *ElasticSketch) lightCol(row int, id string) int {
	return int((common.HashIt(row, []byte(id)) & 0xffffffff) % uint64(es.light.Cols()))
}

func (es *ElasticSketch) lightInsert(id string, count int32) {
	for r := 0; r < es.light.Rows(); r++ {
		es.light.RowSlice(r)[es.lightCol(r, id)] += count
	}
}

func (es *ElasticSketch) lightEstimate(id string) int32 {
	est := es.light.At(0, es.lightCol(0, id))
	for r := 1; r < es.light.Rows(); r++ {
		est = min(est, es.light.At(r, es.lightCol(r, id)))
	}
	return est
}

func (es *ElasticSketch) cloneLocked() *ElasticSketch {
	light, _ := storage.Vector2DFromFn(es.light.Rows(), es.light.Cols(), es.light.At)
	return &ElasticSketch{
		heavy:       slices.Clone(es.heavy),
		bktlen:      es.bktlen,
		light:       light,
		staleCopies: es.staleCopies,
	}
}
