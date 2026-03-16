package elasticsketch

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

const defaultVoteFactor = 8.0
const (
	elasticLightRows = 5
	elasticLightCols = 2048
)

// Config controls Rust-aligned ElasticSketch settings.
type Config struct {
	BucketCount int
}

type bucketSlot struct {
	id    string
	hash  uint64
	count float64
}

type HeavyBucket struct {
	FlowID   string
	VotePos  int
	VoteNeg  int
	Eviction bool
}

func newHeavyBucket() HeavyBucket {
	return HeavyBucket{}
}

func (b *HeavyBucket) evict(id string) {
	b.FlowID = id
	b.VotePos = 1
	b.VoteNeg = 1
	b.Eviction = true
}

// ElasticSketch follows the Rust logic: one heavy bucket per index + CountMin light layer.
// In Go, CountMinSketch is backed by storage.Vector2D-compatible flat storage,
// while the heavy part follows the Rust Vec<HeavyBucket> layout directly.
type ElasticSketch struct {
	cfg    Config
	light  *storage.Vector2D[float64]
	bktlen int

	heavy []HeavyBucket
	mask  uint64
	bits  uint

	mu sync.Mutex
}

// New creates a Rust-aligned Elastic sketch.
func New(cfg Config) (*ElasticSketch, error) {
	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	light, err := storage.InitVector2D[float64](elasticLightRows, elasticLightCols)
	if err != nil {
		return nil, err
	}

	heavy := make([]HeavyBucket, cfg.BucketCount)
	for i := range heavy {
		heavy[i] = newHeavyBucket()
	}

	es := &ElasticSketch{
		cfg:    cfg,
		light:  light,
		bktlen: cfg.BucketCount,
		heavy:  heavy,
		mask:   uint64(elasticLightCols - 1),
		bits:   uint(bits.TrailingZeros(uint(elasticLightCols))),
	}
	return es, nil
}

// Insert registers a single occurrence for the provided key.
func (es *ElasticSketch) Insert(key string) {
	es.InsertN(key, 1)
}

// InsertN records count occurrences for the provided key.
func (es *ElasticSketch) InsertN(key string, count int) {
	if key == "" || count <= 0 {
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()

	for i := 0; i < count; i++ {
		es.insertOne(key)
	}
}

// InsertInput inserts one event from common.SketchInput.
func (es *ElasticSketch) InsertInput(input *common.SketchInput) {
	es.InsertInputN(input, 1)
}

// InsertInputN inserts count events from common.SketchInput.
func (es *ElasticSketch) InsertInputN(input *common.SketchInput, count int) {
	if input == nil || len(input.Bytes) == 0 || count <= 0 {
		return
	}
	es.InsertN(string(input.Bytes), count)
}

// InsertWithHash inserts one event from a pre-hashed value.
// Rust Elastic has only string path; this is a Go adapter.
func (es *ElasticSketch) InsertWithHash(hash uint64) {
	es.InsertWithHashN(hash, 1)
}

// InsertWithHashN inserts count events from pre-hashed value.
func (es *ElasticSketch) InsertWithHashN(hash uint64, count int) {
	if count <= 0 {
		return
	}
	key := fmt.Sprintf("%016x", hash)
	es.InsertN(key, count)
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

func (es *ElasticSketch) insertOne(id string) {
	hash := common.HashIt(common.CanonicalHashSeed, []byte(id))
	idx := int(hash % uint64(es.bktlen))

	heavyBkt := &es.heavy[idx]
	if heavyBkt.FlowID == "" && heavyBkt.VoteNeg == 0 && heavyBkt.VotePos == 0 {
		heavyBkt.FlowID = id
		heavyBkt.VotePos++
		return
	}

	if id == heavyBkt.FlowID {
		heavyBkt.VotePos++
		return
	}

	heavyBkt.VoteNeg++
	if heavyBkt.VotePos > 0 && float64(heavyBkt.VoteNeg)/float64(heavyBkt.VotePos) < defaultVoteFactor {
		es.lightInsertHash(hash)
		return
	}

	vote := heavyBkt.VotePos
	evictedID := heavyBkt.FlowID
	heavyBkt.evict(id)
	for i := 0; i < vote; i++ {
		// Intentionally mirrors current Rust behavior.
		es.lightInsertHash(common.HashIt(common.CanonicalHashSeed, []byte(evictedID)))
	}
}

// Query returns the estimated count for key, following the Rust semantics.
func (es *ElasticSketch) Query(id string) int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.queryLocked(id)
}

func (es *ElasticSketch) queryLocked(id string) int {
	hash := common.HashIt(common.CanonicalHashSeed, []byte(id))
	idx := int(hash % uint64(es.bktlen))

	heavyBkt := es.heavy[idx]

	if id == heavyBkt.FlowID {
		if heavyBkt.Eviction {
			lightResult := int(es.lightEstimateHash(hash))
			return lightResult + heavyBkt.VotePos
		}
		return heavyBkt.VotePos
	}

	return int(es.lightEstimateHash(hash))
}

func (es *ElasticSketch) Merge(other common.Sketch) error {
	o, ok := other.(*ElasticSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	if o == nil {
		return errors.New("cannot merge: nil sketch")
	}
	if es == o {
		return nil
	}
	if es.bktlen != o.bktlen {
		return errors.New("cannot merge: different bucket count")
	}
	if es.light.Rows() != o.light.Rows() || es.light.Cols() != o.light.Cols() {
		return errors.New("cannot merge: different light matrix dimensions")
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()

	es.flushHeavyToLight()
	oHeavy := make([]HeavyBucket, len(o.heavy))
	copy(oHeavy, o.heavy)

	for r := 0; r < es.light.Rows(); r++ {
		leftRow := es.light.RowSlice(r)
		rightRow := o.light.RowSlice(r)
		for c := 0; c < es.light.Cols(); c++ {
			leftRow[c] += rightRow[c]
		}
	}

	for _, bucket := range oHeavy {
		es.spillHeavyToLight(bucket)
	}
	es.resetHeavy()

	return nil
}

// SerializeToBytes serializes ElasticSketch using a Rust-aligned state model.
func (es *ElasticSketch) SerializeToBytes() ([]byte, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	lightBytes, err := es.light.SerializeToBytes()
	if err != nil {
		return nil, err
	}

	type snapshot struct {
		Config Config
		Heavy  []HeavyBucket
		Light  []byte
	}

	return common.EncodeToBytes(snapshot{
		Config: es.cfg,
		Heavy:  es.heavy,
		Light:  lightBytes,
	})
}

// DeserializeElasticSketchFromBytes restores ElasticSketch state.
func DeserializeElasticSketchFromBytes(data []byte) (*ElasticSketch, error) {
	type snapshot struct {
		Config Config
		Heavy  []HeavyBucket
		Light  []byte
	}

	var snap snapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if err := snap.Config.normalize(); err != nil {
		return nil, err
	}
	if len(snap.Heavy) != snap.Config.BucketCount {
		return nil, errors.New("invalid snapshot heavy bucket length")
	}

	light, err := storage.DeserializeVector2DFromBytes[float64](snap.Light)
	if err != nil {
		return nil, err
	}

	heavy := make([]HeavyBucket, len(snap.Heavy))
	copy(heavy, snap.Heavy)

	es := &ElasticSketch{
		cfg:    snap.Config,
		light:  light,
		bktlen: snap.Config.BucketCount,
		heavy:  heavy,
		mask:   uint64(light.Cols() - 1),
		bits:   light.MaskBits(),
	}
	return es, nil
}

func (cfg *Config) normalize() error {
	if cfg.BucketCount <= 0 {
		return fmt.Errorf("BucketCount must be positive")
	}
	return nil
}

// debugBucketSlot exposes heavy-bucket state as a single-slot view for tests.
func (es *ElasticSketch) debugBucketSlot(bucketIdx, slotIdx int) bucketSlot {
	if bucketIdx < 0 || bucketIdx >= es.bktlen {
		return bucketSlot{}
	}
	if slotIdx != 0 {
		return bucketSlot{}
	}
	flow := es.heavy[bucketIdx].FlowID
	return bucketSlot{
		id:    flow,
		hash:  common.HashIt(common.CanonicalHashSeed, []byte(flow)),
		count: float64(es.heavy[bucketIdx].VotePos),
	}
}

func (es *ElasticSketch) debugBucketVote(bucketIdx int) float64 {
	if bucketIdx < 0 || bucketIdx >= es.bktlen {
		return 0
	}
	return float64(es.heavy[bucketIdx].VoteNeg)
}

func (es *ElasticSketch) lightInsertHash(hash uint64) {
	shift := uint(0)
	for r := 0; r < es.light.Rows(); r++ {
		c := int((hash >> shift) & es.mask)
		if c >= es.light.Cols() {
			c %= es.light.Cols()
		}
		es.light.Add(r, c, 1.0)
		shift += es.bits
	}
}

func (es *ElasticSketch) lightEstimateHash(hash uint64) float64 {
	minVal := 0.0
	shift := uint(0)
	for r := 0; r < es.light.Rows(); r++ {
		c := int((hash >> shift) & es.mask)
		if c >= es.light.Cols() {
			c %= es.light.Cols()
		}
		val := es.light.At(r, c)
		if r == 0 || val < minVal {
			minVal = val
		}
		shift += es.bits
	}
	return minVal
}

func (es *ElasticSketch) lightInsertHashN(hash uint64, count int) {
	for i := 0; i < count; i++ {
		es.lightInsertHash(hash)
	}
}

func (es *ElasticSketch) spillHeavyToLight(bucket HeavyBucket) {
	if bucket.FlowID == "" || bucket.VotePos <= 0 {
		return
	}
	hash := common.HashIt(common.CanonicalHashSeed, []byte(bucket.FlowID))
	es.lightInsertHashN(hash, bucket.VotePos)
}

func (es *ElasticSketch) flushHeavyToLight() {
	for _, bucket := range es.heavy {
		es.spillHeavyToLight(bucket)
	}
}

func (es *ElasticSketch) resetHeavy() {
	for i := range es.heavy {
		es.heavy[i] = newHeavyBucket()
	}
}
