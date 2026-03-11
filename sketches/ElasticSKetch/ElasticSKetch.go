package elasticsketch

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

const (
	defaultVoteFactor    = 8.0
	defaultFlushChanSize = 128
	defaultH1Seed        = 0x9e3779b97f4a7c15
	defaultH2Seed        = 0x243f6a8885a308d3
)

// FlushFunc is invoked every time the sketch emits a batched update.
type FlushFunc func(entry ElasticEntry)

// Config captures all tunables for an ElasticSketch instance.
type Config struct {
	BucketCount    int     // number of buckets in the exact layer
	SlotsPerBucket int     // number of slots per bucket
	SketchSize     int     // number of cells in the approximate layer
	VoteFactor     float64 // multiplier applied to the weakest slot before eviction
	FlushThreshold float64 // threshold that triggers a flush when the accumulated count reaches this value
	H1Seed         uint64  // seed mixed into bucket hash derivation
	H2Seed         uint64  // seed mixed into sketch hash derivation
	FlushChanSize  int     // optional buffer size for the internal flush channel
}

// ElasticEntry is emitted whenever the sketch flushes a batched update.
type ElasticEntry struct {
	ID         string
	Pos        int
	Count      float64
	FromBucket bool
}

type bucketSlot struct {
	id    string
	hash  uint64
	count float64
}

type bucket struct {
	slots []bucketSlot
	vote  float64
}

// ElasticSketch implements a two-layer heavy-hitter tracker.
type ElasticSketch struct {
	cfg Config

	// Flattened exact layer storage backed by Vector1D wrappers.
	slotIDs    *storage.Vector1D[string]
	slotHashes *storage.Vector1D[uint64]
	slotCounts *storage.Vector1D[float64]
	votes      *storage.Vector1D[float64]

	// Approximate layer storage.
	sketch *storage.Vector1D[float64]

	flushFn   FlushFunc
	flushCh   chan ElasticEntry
	closeOnce sync.Once
	mu        sync.Mutex
}

// New creates a new ElasticSketch with the supplied configuration. The returned sketch is safe for concurrent use.
func New(cfg Config, opts ...Option) (*ElasticSketch, error) {
	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	slotTotal := cfg.BucketCount * cfg.SlotsPerBucket
	slotIDs, err := storage.FilledVector1D[string](slotTotal, "")
	if err != nil {
		return nil, err
	}
	slotHashes, err := storage.FilledVector1D[uint64](slotTotal, 0)
	if err != nil {
		return nil, err
	}
	slotCounts, err := storage.FilledVector1D[float64](slotTotal, 0)
	if err != nil {
		return nil, err
	}
	votes, err := storage.FilledVector1D[float64](cfg.BucketCount, 0)
	if err != nil {
		return nil, err
	}
	sketchStore, err := storage.FilledVector1D[float64](cfg.SketchSize, 0)
	if err != nil {
		return nil, err
	}

	es := &ElasticSketch{
		cfg:        cfg,
		slotIDs:    slotIDs,
		slotHashes: slotHashes,
		slotCounts: slotCounts,
		votes:      votes,
		sketch:     sketchStore,
	}

	for _, opt := range opts {
		opt(es)
	}

	if es.flushFn == nil {
		es.flushCh = make(chan ElasticEntry, cfg.FlushChanSize)
	}

	return es, nil
}

// Option customises behaviour for a new ElasticSketch.
type Option func(*ElasticSketch)

// WithFlushFunc installs a custom flush handler. When provided the ElasticSketch will not create an internal channel.
func WithFlushFunc(fn FlushFunc) Option {
	return func(es *ElasticSketch) {
		es.flushFn = fn
	}
}

// FlushChannel exposes the internal channel used for batched updates. It returns nil if a custom FlushFunc is installed.
func (es *ElasticSketch) FlushChannel() <-chan ElasticEntry {
	return es.flushCh
}

// Close closes the internal flush channel, if present. It is safe to call multiple times.
func (es *ElasticSketch) Close() {
	if es.flushCh == nil {
		return
	}
	es.closeOnce.Do(func() {
		close(es.flushCh)
	})
}

// Insert registers a single occurrence for the provided key.
func (es *ElasticSketch) Insert(key string) {
	es.InsertN(key, 1)
}

// InsertN records count occurrences for the provided key. A non-positive count is ignored.
func (es *ElasticSketch) InsertN(key string, count int) {
	if count <= 0 || key == "" {
		return
	}
	es.InsertInputN(common.FromString(key), count)
}

// InsertInput inserts one event from common.SketchInput.
func (es *ElasticSketch) InsertInput(input *common.SketchInput) {
	es.InsertInputN(input, 1)
}

// InsertInputN inserts count events from common.SketchInput.
func (es *ElasticSketch) InsertInputN(input *common.SketchInput, count int) {
	if input == nil || count <= 0 {
		return
	}
	if len(input.Bytes) == 0 {
		return
	}

	key := string(input.Bytes)
	hash := input.Hash

	var emitted []ElasticEntry

	es.mu.Lock()
	for i := 0; i < count; i++ {
		es.insertOneHashed(key, hash, &emitted)
	}
	es.mu.Unlock()

	for _, entry := range emitted {
		es.emit(entry)
	}
}

// InsertWithHash inserts one event from pre-hashed value (fast path).
func (es *ElasticSketch) InsertWithHash(hash uint64) {
	es.InsertWithHashN(hash, 1)
}

// InsertWithHashN inserts count events from pre-hashed value (fast path).
func (es *ElasticSketch) InsertWithHashN(hash uint64, count int) {
	if count <= 0 {
		return
	}

	var emitted []ElasticEntry

	es.mu.Lock()
	for i := 0; i < count; i++ {
		es.insertOneHashed("", hash, &emitted)
	}
	es.mu.Unlock()

	for _, entry := range emitted {
		es.emit(entry)
	}
}

func (es *ElasticSketch) insertOneHashed(key string, hash uint64, emitted *[]ElasticEntry) {
	bucketIdx := es.bucketIndex(hash)

	if es.cfg.SlotsPerBucket == 0 {
		return
	}

	bucketStart := bucketIdx * es.cfg.SlotsPerBucket
	bucketEnd := bucketStart + es.cfg.SlotsPerBucket

	ids := es.slotIDs.AsMutSlice()
	hashes := es.slotHashes.AsMutSlice()
	counts := es.slotCounts.AsMutSlice()
	votes := es.votes.AsMutSlice()

	// Exact layer lookup.
	if key != "" {
		for i := bucketStart; i < bucketEnd; i++ {
			if ids[i] == key {
				counts[i]++
				if counts[i] >= es.cfg.FlushThreshold {
					*emitted = append(*emitted, ElasticEntry{
						ID:         key,
						Pos:        bucketIdx,
						Count:      counts[i],
						FromBucket: true,
					})
					counts[i] = 0
				}
				return
			}
		}
	} else {
		for i := bucketStart; i < bucketEnd; i++ {
			if hashes[i] == hash && counts[i] > 0 {
				counts[i]++
				if counts[i] >= es.cfg.FlushThreshold {
					*emitted = append(*emitted, ElasticEntry{
						ID:         ids[i],
						Pos:        bucketIdx,
						Count:      counts[i],
						FromBucket: true,
					})
					counts[i] = 0
				}
				return
			}
		}
	}

	// Track weakest slot in bucket.
	minAbsIdx := bucketStart
	minVal := counts[bucketStart]
	for i := bucketStart + 1; i < bucketEnd; i++ {
		if counts[i] < minVal {
			minAbsIdx = i
			minVal = counts[i]
		}
	}

	pressure := votes[bucketIdx] + 1
	threshold := es.cfg.VoteFactor * minVal

	if pressure >= threshold {
		votes[bucketIdx] = 0
		if ids[minAbsIdx] != "" && counts[minAbsIdx] > 0 {
			es.addToSketchHashed(ids[minAbsIdx], hashes[minAbsIdx], counts[minAbsIdx], emitted)
		}
		ids[minAbsIdx] = key
		hashes[minAbsIdx] = hash
		counts[minAbsIdx] = 1

		if counts[minAbsIdx] >= es.cfg.FlushThreshold {
			*emitted = append(*emitted, ElasticEntry{
				ID:         key,
				Pos:        bucketIdx,
				Count:      counts[minAbsIdx],
				FromBucket: true,
			})
			counts[minAbsIdx] = 0
		}
		return
	}

	votes[bucketIdx] = pressure
	es.addToSketchHashed(key, hash, 1, emitted)
}

func (es *ElasticSketch) addToSketchHashed(key string, hash uint64, delta float64, emitted *[]ElasticEntry) {
	cells := es.sketch.AsMutSlice()
	if len(cells) == 0 {
		return
	}

	idx := es.sketchIndex(hash)
	cells[idx] += delta

	if cells[idx] >= es.cfg.FlushThreshold {
		*emitted = append(*emitted, ElasticEntry{
			ID:         key,
			Pos:        idx,
			Count:      cells[idx],
			FromBucket: false,
		})
		cells[idx] = 0
	}
}

func (es *ElasticSketch) emit(entry ElasticEntry) {
	if es.flushFn != nil {
		es.flushFn(entry)
		return
	}
	if es.flushCh != nil {
		es.flushCh <- entry
	}
}

func (es *ElasticSketch) bucketIndex(hash uint64) int {
	mixed := mix64(hash ^ es.cfg.H1Seed)
	return int(mixed % uint64(es.cfg.BucketCount))
}

func (es *ElasticSketch) sketchIndex(hash uint64) int {
	mixed := mix64(hash ^ es.cfg.H2Seed)
	return int(mixed % uint64(es.cfg.SketchSize))
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (cfg *Config) normalize() error {
	if cfg.BucketCount <= 0 {
		return fmt.Errorf("BucketCount must be positive")
	}
	if cfg.SlotsPerBucket <= 0 {
		return fmt.Errorf("SlotsPerBucket must be positive")
	}
	if cfg.SketchSize <= 0 {
		return fmt.Errorf("SketchSize must be positive")
	}
	if cfg.FlushThreshold <= 0 {
		return fmt.Errorf("FlushThreshold must be positive")
	}
	if cfg.VoteFactor <= 0 {
		cfg.VoteFactor = defaultVoteFactor
	}
	if cfg.FlushChanSize < 0 {
		return fmt.Errorf("FlushChanSize cannot be negative")
	}
	if cfg.FlushChanSize == 0 {
		cfg.FlushChanSize = defaultFlushChanSize
	}
	if cfg.H1Seed == 0 {
		cfg.H1Seed = defaultH1Seed
	}
	if cfg.H2Seed == 0 {
		cfg.H2Seed = defaultH2Seed
	}

	return nil
}

type elasticSketchSnapshot struct {
	Version int
	Config  Config

	SlotIDs    []string
	SlotHashes []uint64
	SlotCounts []float64
	Votes      []float64
	Sketch     []float64
}

type legacyElasticSketchSnapshot struct {
	Config  Config
	Buckets []bucket
	Sketch  []float64
}

// SerializeToBytes serializes ElasticSketch into bytes.
func (es *ElasticSketch) SerializeToBytes() ([]byte, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	return common.EncodeToBytes(elasticSketchSnapshot{
		Version:    1,
		Config:     es.cfg,
		SlotIDs:    append([]string(nil), es.slotIDs.AsSlice()...),
		SlotHashes: append([]uint64(nil), es.slotHashes.AsSlice()...),
		SlotCounts: append([]float64(nil), es.slotCounts.AsSlice()...),
		Votes:      append([]float64(nil), es.votes.AsSlice()...),
		Sketch:     append([]float64(nil), es.sketch.AsSlice()...),
	})
}

// DeserializeElasticSketchFromBytes restores ElasticSketch from serialized bytes.
func DeserializeElasticSketchFromBytes(data []byte) (*ElasticSketch, error) {
	var snap elasticSketchSnapshot
	if err := common.DecodeFromBytes(data, &snap); err == nil {
		if err := snap.Config.normalize(); err != nil {
			return nil, err
		}
		slotTotal := snap.Config.BucketCount * snap.Config.SlotsPerBucket
		if len(snap.SlotIDs) != slotTotal {
			return nil, errors.New("invalid snapshot slot ids length")
		}
		if len(snap.SlotHashes) != slotTotal {
			return nil, errors.New("invalid snapshot slot hashes length")
		}
		if len(snap.SlotCounts) != slotTotal {
			return nil, errors.New("invalid snapshot slot counts length")
		}
		if len(snap.Votes) != snap.Config.BucketCount {
			return nil, errors.New("invalid snapshot votes length")
		}
		if len(snap.Sketch) != snap.Config.SketchSize {
			return nil, errors.New("invalid snapshot sketch size")
		}

		es := &ElasticSketch{
			cfg:        snap.Config,
			slotIDs:    storage.Vector1DFromSlice(snap.SlotIDs),
			slotHashes: storage.Vector1DFromSlice(snap.SlotHashes),
			slotCounts: storage.Vector1DFromSlice(snap.SlotCounts),
			votes:      storage.Vector1DFromSlice(snap.Votes),
			sketch:     storage.Vector1DFromSlice(snap.Sketch),
		}
		es.flushCh = make(chan ElasticEntry, snap.Config.FlushChanSize)
		return es, nil
	}

	// Legacy snapshot format fallback
	var legacy legacyElasticSketchSnapshot
	if err := common.DecodeFromBytes(data, &legacy); err != nil {
		return nil, err
	}
	if err := legacy.Config.normalize(); err != nil {
		return nil, err
	}
	slotTotal := legacy.Config.BucketCount * legacy.Config.SlotsPerBucket
	if len(legacy.Buckets) != legacy.Config.BucketCount {
		return nil, errors.New("invalid legacy snapshot buckets length")
	}
	if len(legacy.Sketch) != legacy.Config.SketchSize {
		return nil, errors.New("invalid legacy snapshot sketch size")
	}

	slotIDs, err := storage.FilledVector1D[string](slotTotal, "")
	if err != nil {
		return nil, err
	}
	slotHashes, err := storage.FilledVector1D[uint64](slotTotal, 0)
	if err != nil {
		return nil, err
	}
	slotCounts, err := storage.FilledVector1D[float64](slotTotal, 0)
	if err != nil {
		return nil, err
	}
	votes, err := storage.FilledVector1D[float64](legacy.Config.BucketCount, 0)
	if err != nil {
		return nil, err
	}

	for b := 0; b < legacy.Config.BucketCount; b++ {
		if len(legacy.Buckets[b].slots) != legacy.Config.SlotsPerBucket {
			return nil, errors.New("invalid legacy snapshot slot count")
		}
		votes.AsMutSlice()[b] = legacy.Buckets[b].vote
		base := b * legacy.Config.SlotsPerBucket
		for s := 0; s < legacy.Config.SlotsPerBucket; s++ {
			slot := legacy.Buckets[b].slots[s]
			slotIDs.AsMutSlice()[base+s] = slot.id
			slotHashes.AsMutSlice()[base+s] = common.FromString(slot.id).Hash
			slotCounts.AsMutSlice()[base+s] = slot.count
		}
	}

	es := &ElasticSketch{
		cfg:        legacy.Config,
		slotIDs:    slotIDs,
		slotHashes: slotHashes,
		slotCounts: slotCounts,
		votes:      votes,
		sketch:     storage.Vector1DFromSlice(legacy.Sketch),
	}
	es.flushCh = make(chan ElasticEntry, legacy.Config.FlushChanSize)
	return es, nil
}

// debugBucketSlot exposes slot state for tests inside this package.
func (es *ElasticSketch) debugBucketSlot(bucketIdx, slotIdx int) bucketSlot {
	idx := bucketIdx*es.cfg.SlotsPerBucket + slotIdx
	ids := es.slotIDs.AsSlice()
	hashes := es.slotHashes.AsSlice()
	counts := es.slotCounts.AsSlice()
	return bucketSlot{id: ids[idx], hash: hashes[idx], count: counts[idx]}
}

func (es *ElasticSketch) debugBucketVote(bucketIdx int) float64 {
	return es.votes.AsSlice()[bucketIdx]
}
