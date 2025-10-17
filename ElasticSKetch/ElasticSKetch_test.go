package elasticsketch

import (
	"flag"
	"os"
	"testing"

	"github.com/golang/glog"
)

func TestMain(m *testing.M) {
	_ = flag.Set("alsologtostderr", "true")
	flag.Parse()
	code := m.Run()
	glog.Flush()
	os.Exit(code)
}

func TestElasticSketchBucketFlush(t *testing.T) {
	cfg := Config{
		BucketCount:    1,
		SlotsPerBucket: 2,
		SketchSize:     8,
		VoteFactor:     2,
		FlushThreshold: 3,
	}

	var entries []ElasticEntry
	es, err := New(cfg, WithFlushFunc(func(entry ElasticEntry) {
		entries = append(entries, entry)
	}))
	if err != nil {
		t.Fatalf("unexpected error constructing sketch: %v", err)
	}

	es.InsertN("foo", 3)
	glog.Infof("TestElasticSketchBucketFlush: entries after insert: %+v", entries)

	if len(entries) != 1 {
		t.Fatalf("expected 1 flush entry, got %d", len(entries))
	}

	entry := entries[0]
	if !entry.FromBucket {
		t.Fatalf("expected flush from bucket, got sketch entry: %#v", entry)
	}
	if entry.ID != "foo" {
		t.Fatalf("expected flush for key foo, got %s", entry.ID)
	}
	if entry.Count != 3 {
		t.Fatalf("expected flush count 3, got %f", entry.Count)
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	slot := es.buckets[0].slots[0]
	if slot.id != "foo" {
		t.Fatalf("expected bucket slot to keep key foo, got %q", slot.id)
	}
	glog.Infof("TestElasticSketchBucketFlush: bucket slot after flush id=%s count=%f", slot.id, slot.count)
	if slot.count != 0 {
		t.Fatalf("expected bucket slot count reset after flush, got %f", slot.count)
	}
}

func TestElasticSketchEvictionAndSketchFlush(t *testing.T) {
	cfg := Config{
		BucketCount:    1,
		SlotsPerBucket: 1,
		SketchSize:     16,
		VoteFactor:     3,
		FlushThreshold: 3,
	}

	var entries []ElasticEntry
	es, err := New(cfg, WithFlushFunc(func(entry ElasticEntry) {
		entries = append(entries, entry)
	}))
	if err != nil {
		t.Fatalf("unexpected error constructing sketch: %v", err)
	}

	es.InsertN("heavy", 2)
	glog.Infof("TestElasticSketchEvictionAndSketchFlush: entries after heavy inserts: %+v", entries)

	es.InsertN("light", 6)
	glog.Infof("TestElasticSketchEvictionAndSketchFlush: entries after light inserts: %+v", entries)

	es.mu.Lock()
	slot := es.buckets[0].slots[0]
	es.mu.Unlock()
	glog.Infof("TestElasticSketchEvictionAndSketchFlush: slot after eviction id=%s count=%f vote=%f", slot.id, slot.count, es.buckets[0].vote)

	if slot.id != "light" {
		t.Fatalf("expected bucket slot to be occupied by light, got %q", slot.id)
	}
	if slot.count != 1 {
		t.Fatalf("expected bucket slot count 1 after eviction, got %f", slot.count)
	}

	foundSketchFlush := false
	for _, entry := range entries {
		if !entry.FromBucket && entry.ID == "light" && entry.Count == cfg.FlushThreshold {
			foundSketchFlush = true
			break
		}
	}
	if !foundSketchFlush {
		t.Fatalf("expected sketch flush for key light, got %#v", entries)
	}

	prevEntries := len(entries)
	es.InsertN("light", 2)
	glog.Infof("TestElasticSketchEvictionAndSketchFlush: entries after additional light inserts: %+v", entries)
	if len(entries) != prevEntries+1 {
		t.Fatalf("expected an additional flush entry from bucket, got %d total", len(entries))
	}

	last := entries[len(entries)-1]
	if !last.FromBucket {
		t.Fatalf("expected last entry to come from bucket, got %#v", last)
	}
	if last.ID != "light" {
		t.Fatalf("expected bucket flush for key light, got %s", last.ID)
	}
	if last.Count != cfg.FlushThreshold {
		t.Fatalf("expected bucket flush count %f, got %f", cfg.FlushThreshold, last.Count)
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	glog.Infof("TestElasticSketchEvictionAndSketchFlush: bucket slot final state id=%s count=%f", es.buckets[0].slots[0].id, es.buckets[0].slots[0].count)
	if es.buckets[0].slots[0].count != 0 {
		t.Fatalf("expected bucket count reset after flush, got %f", es.buckets[0].slots[0].count)
	}
}
