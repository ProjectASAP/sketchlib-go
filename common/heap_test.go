package common

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTopKHeap_Basic verifies initialization and basic population
func TestTopKHeap_Basic(t *testing.T) {
	k := 5
	h := NewTopKHeap(k)

	assert.Equal(t, k, h.K)
	assert.Equal(t, 0, len(h.Heap))

	// Insert items fewer than K
	h.Update("A", 10)
	h.Update("B", 20)
	h.Update("C", 5)

	assert.Equal(t, 3, len(h.Heap))

	// Check Root (Must be the smallest item because this is a Min-Heap for Top-K)
	// Root is the gatekeeper for entering Top-K
	assert.Equal(t, int64(5), h.Heap[0].Count, "Root must be the smallest value (5)")
	assert.Equal(t, "C", h.Heap[0].Key)
}

// TestTopKHeap_Eviction verifies Top-K logic
// New item only enters if > Root (Min), and replaces Root.
func TestTopKHeap_Eviction(t *testing.T) {
	k := 3
	h := NewTopKHeap(k)

	// Fill completely: [10, 20, 30]
	// Min-Heap structure expectation: 10 at root
	h.Update("A", 10)
	h.Update("B", 20)
	h.Update("C", 30)

	requireHeapProperty(t, h)
	assert.Equal(t, int64(10), h.Heap[0].Count, "Initial root must be 10")

	// 1. Insert SMALL item (5) -> Must be REJECTED
	h.Update("D", 5)
	_, found := h.Find("D")
	assert.False(t, found, "Small item (5) must not enter because < Root (10)")
	assert.Equal(t, int64(10), h.Heap[0].Count)

	// 2. Insert LARGE item (15) -> Must REPLACE Root (10)
	// New heap should contain: 15, 20, 30. New Root (Min) = 15
	h.Update("E", 15)

	_, foundE := h.Find("E")
	assert.True(t, foundE, "Large item (15) must enter")

	_, foundA := h.Find("A")
	assert.False(t, foundA, "Item A (10) should be evicted")

	assert.Equal(t, int64(15), h.Heap[0].Count, "New root must be 15")
	requireHeapProperty(t, h)

	// 3. Insert VERY LARGE item (100) -> Replaces Root (15)
	// Heap items: 20, 30, 100. New Root (Min) = 20
	h.Update("F", 100)
	assert.Equal(t, int64(20), h.Heap[0].Count, "New root must be 20")
}

// TestTopKHeap_UpdateExisting verifies updating count for an existing key
func TestTopKHeap_UpdateExisting(t *testing.T) {
	h := NewTopKHeap(3)

	h.Update("A", 10)
	h.Update("B", 20)
	h.Update("C", 30)

	// Update A (Root/10) to 50.
	// Heap items become: 50, 20, 30. New Min -> 20.
	updated := h.Update("A", 50)
	assert.True(t, updated)

	idx, found := h.Find("A")
	assert.True(t, found)
	assert.Equal(t, int64(50), h.Heap[idx].Count)

	// Verify Root changes
	assert.Equal(t, int64(20), h.Heap[0].Count, "After A increases to 50, Root (min) should be B (20)")
	requireHeapProperty(t, h)
}

// TestTopKHeap_Copy verifies NewTopKFromHeap function
func TestTopKHeap_Copy(t *testing.T) {
	h1 := NewTopKHeap(3)
	h1.Update("A", 10)
	h1.Update("B", 20)

	// Copy
	h2 := NewTopKFromHeap(h1)

	// Ensure contents are the same
	assert.Equal(t, len(h1.Heap), len(h2.Heap))
	assert.Equal(t, h1.Heap[0].Key, h2.Heap[0].Key)

	// Modify h2, h1 must not change
	h2.Update("C", 30)

	assert.Equal(t, 2, len(h1.Heap), "H1 size must not change")
	assert.Equal(t, 3, len(h2.Heap), "H2 size must increase")

	// Modify item value in h2
	h2.Update("A", 999)
	idx1, _ := h1.Find("A")
	idx2, _ := h2.Find("A")

	assert.Equal(t, int64(10), h1.Heap[idx1].Count, "H1 count must not change")
	assert.Equal(t, int64(999), h2.Heap[idx2].Count, "H2 count must change")
}

// TestTopKHeap_Correctness verifies Min-Heap property with random data
func TestTopKHeap_Correctness(t *testing.T) {
	k := 10
	h := NewTopKHeap(k)

	// Insert random data
	items := []int{50, 10, 20, 5, 100, 2, 80, 40, 30, 90, 200, 1}

	for i, val := range items {
		key := string(rune('A' + i)) // Unique keys A, B, C...
		h.Update(key, int64(val))
	}

	// Top-K Min-Heap Logic:
	// 1. Heap must be full (k=10)
	assert.Equal(t, k, len(h.Heap))

	// 2. Find Top-K manually from input
	sort.Ints(items) // [1, 2, 5, 10, 20, 30, 40, 50, 80, 90, 100, 200]
	// Top 10 largest are: 5...200
	// Smallest value among Top 10 is 5.

	// 3. Root must be 5
	assert.Equal(t, int64(5), h.Heap[0].Count, "Heap root must be the smallest value of the Top-K set")

	// 4. Ensure all elements in heap >= root
	for _, item := range h.Heap {
		assert.GreaterOrEqual(t, item.Count, h.Heap[0].Count)
	}
}

// Helper: Ensure heap property (Parent <= Children)
func requireHeapProperty(t *testing.T, h *TopKHeap) {
	for i, item := range h.Heap {
		l := 2*i + 1
		r := 2*i + 2

		if l < len(h.Heap) {
			assert.LessOrEqual(t, item.Count, h.Heap[l].Count, "Parent %d (%d) > Left Child %d (%d)", i, item.Count, l, h.Heap[l].Count)
		}
		if r < len(h.Heap) {
			assert.LessOrEqual(t, item.Count, h.Heap[r].Count, "Parent %d (%d) > Right Child %d (%d)", i, item.Count, r, h.Heap[r].Count)
		}
	}
}
