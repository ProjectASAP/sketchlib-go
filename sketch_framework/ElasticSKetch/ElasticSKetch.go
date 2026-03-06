package elasticsketch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/approx-telemetry/sketchlib-go/common"
	"github.com/cespare/xxhash/v2"
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
	FlushThreshold float64 // Threshold that triggers a flush when the accumulated count reaches this value
	H1Seed         uint64  // seed for the bucket hash
	H2Seed         uint64  // seed for the sketch hash
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
	count float64
}

type bucket struct {
	slots []bucketSlot
	vote  float64
}

// ElasticSketch implements the two-layer heavy-hitter tracking described in the design doc.
type ElasticSketch struct {
	cfg       Config
	buckets   []bucket
	sketch    []float64
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

	es := &ElasticSketch{
		cfg:     cfg,
		buckets: make([]bucket, cfg.BucketCount),
		sketch:  make([]float64, cfg.SketchSize),
	}

	for i := range es.buckets {
		es.buckets[i].slots = make([]bucketSlot, cfg.SlotsPerBucket)
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

	var emitted []ElasticEntry

	es.mu.Lock()
	for i := 0; i < count; i++ {
		es.insertOne(key, &emitted)
	}
	es.mu.Unlock()

	for _, entry := range emitted {
		es.emit(entry)
	}
}

func (es *ElasticSketch) insertOne(key string, emitted *[]ElasticEntry) {
	bucketIdx := int(es.hash1(key) % uint64(len(es.buckets)))
	b := &es.buckets[bucketIdx]

	if len(b.slots) == 0 {
		return
	}

	// Exact layer lookup.
	for i := range b.slots {
		slot := &b.slots[i]
		if slot.id == key {
			slot.count++
			if slot.count >= es.cfg.FlushThreshold {
				*emitted = append(*emitted, ElasticEntry{
					ID:         key,
					Pos:        bucketIdx,
					Count:      slot.count,
					FromBucket: true,
				})
				slot.count = 0
			}
			return
		}
	}

	// Track weakest slot.
	minIdx := 0
	minVal := b.slots[0].count
	for i := 1; i < len(b.slots); i++ {
		if b.slots[i].count < minVal {
			minIdx = i
			minVal = b.slots[i].count
		}
	}

	pressure := b.vote + 1
	threshold := es.cfg.VoteFactor * minVal

	if pressure >= threshold {
		b.vote = 0
		slot := &b.slots[minIdx]
		if slot.id != "" && slot.count > 0 {
			es.addToSketch(slot.id, slot.count, emitted)
		}
		slot.id = key
		slot.count = 1

		if slot.count >= es.cfg.FlushThreshold {
			*emitted = append(*emitted, ElasticEntry{
				ID:         key,
				Pos:        bucketIdx,
				Count:      slot.count,
				FromBucket: true,
			})
			slot.count = 0
		}
		return
	}

	b.vote = pressure
	es.addToSketch(key, 1, emitted)
}

func (es *ElasticSketch) addToSketch(key string, delta float64, emitted *[]ElasticEntry) {
	if len(es.sketch) == 0 {
		return
	}

	idx := int(es.hash2(key) % uint64(len(es.sketch)))
	es.sketch[idx] += delta

	if es.sketch[idx] >= es.cfg.FlushThreshold {
		*emitted = append(*emitted, ElasticEntry{
			ID:         key,
			Pos:        idx,
			Count:      es.sketch[idx],
			FromBucket: false,
		})
		es.sketch[idx] = 0
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

func (es *ElasticSketch) hash1(key string) uint64 {
	return hashWithSeed(key, es.cfg.H1Seed)
}

func (es *ElasticSketch) hash2(key string) uint64 {
	return hashWithSeed(key, es.cfg.H2Seed)
}

func hashWithSeed(key string, seed uint64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], seed)
	h := xxhash.New()
	// It is safe to ignore the error from h.Write because xxhash.New().Write() never returns a non-nil error.
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(key))
	return h.Sum64()
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
	Config  Config
	Buckets []bucket
	Sketch  []float64
}

// SerializeToBytes serializes ElasticSketch into bytes.
func (es *ElasticSketch) SerializeToBytes() ([]byte, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	return common.EncodeToBytes(elasticSketchSnapshot{
		Config:  es.cfg,
		Buckets: es.buckets,
		Sketch:  es.sketch,
	})
}

// DeserializeElasticSketchFromBytes restores ElasticSketch from serialized bytes.
func DeserializeElasticSketchFromBytes(data []byte) (*ElasticSketch, error) {
	var snap elasticSketchSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if err := snap.Config.normalize(); err != nil {
		return nil, err
	}
	if len(snap.Buckets) != snap.Config.BucketCount {
		return nil, errors.New("invalid snapshot buckets length")
	}
	if len(snap.Sketch) != snap.Config.SketchSize {
		return nil, errors.New("invalid snapshot sketch size")
	}
	for i := range snap.Buckets {
		if len(snap.Buckets[i].slots) != snap.Config.SlotsPerBucket {
			return nil, errors.New("invalid snapshot slot count")
		}
	}

	es := &ElasticSketch{
		cfg:     snap.Config,
		buckets: snap.Buckets,
		sketch:  snap.Sketch,
	}
	es.flushCh = make(chan ElasticEntry, snap.Config.FlushChanSize)
	return es, nil
}
