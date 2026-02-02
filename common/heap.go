package common

import (
	"fmt"
)

type Item struct {
	Key   string
	Count int64
}

type TopKHeap struct {
	Heap     []Item
	K        int
	totalMem float64
}

func (topkheap *TopKHeap) Clean() {
	topkheap.Heap = topkheap.Heap[:0]
}

func (topkheap *TopKHeap) GetMemoryBytes() float64 {
	return topkheap.totalMem
}

func NewTopKHeap(k int) (topkheap *TopKHeap) {
	topkheap = &TopKHeap{
		Heap:     make([]Item, 0, k),
		K:        k,
		totalMem: 0,
	}
	return topkheap
}

func NewTopKFromHeap(from *TopKHeap) (topkheap *TopKHeap) {
	topkheap = &TopKHeap{
		K:    from.K,
		Heap: make([]Item, len(from.Heap)),
	}
	for i, item := range from.Heap {
		topkheap.Heap[i].Key = item.Key
		topkheap.Heap[i].Count = item.Count
	}

	return topkheap
}

func (topkheap *TopKHeap) Print() {
	for _, item := range topkheap.Heap {
		fmt.Println(item.Key, ":", item.Count)
	}
}

func (topkheap *TopKHeap) Find(key string) (int, bool) {
	for i, item := range topkheap.Heap {
		if item.Key == key {
			return i, true
		}
	}
	return -1, false
}

func (topkheap *TopKHeap) leftChild(i int) int {
	return 2*i + 1
}

func (topkheap *TopKHeap) rightChild(i int) int {
	return 2*i + 2
}

func (topkheap *TopKHeap) parent(i int) int {
	return (i - 1) / 2
}

func (topkheap *TopKHeap) swap(i, j int) {
	var key string
	var count int64
	key = topkheap.Heap[i].Key
	topkheap.Heap[i].Key = topkheap.Heap[j].Key
	topkheap.Heap[j].Key = key

	count = topkheap.Heap[i].Count
	topkheap.Heap[i].Count = topkheap.Heap[j].Count
	topkheap.Heap[j].Count = count
}

// update modifies the priority and value of an Item in the queue.
func (topkheap *TopKHeap) UpdateCS(key string, count int64) bool {

	index, find := topkheap.Find(key)

	if find {
		topkheap.Heap[index].Count = topkheap.Heap[index].Count + 1
		topkheap.updateOrder(index)
		return true
	} else {
		topkheap.Insert(key, count)
		return true
	}
}

// update modifies the priority and value of an Item in the queue.
func (topkheap *TopKHeap) Update(key string, count int64) bool {

	index, find := topkheap.Find(key)

	if find {
		topkheap.Heap[index].Count = count
		topkheap.updateOrder(index)
		return true
	} else {
		topkheap.Insert(key, count)
		return true
	}
}

func (topkheap *TopKHeap) Insert(key string, count int64) {
	if int(len(topkheap.Heap)) < topkheap.K {
		topkheap.Heap = append(topkheap.Heap, Item{
			Key:   key,
			Count: count,
		})
		topkheap.totalMem += float64(len(key)) + 8
		topkheap.updateOrderUp(len(topkheap.Heap) - 1)
	} else {
		if topkheap.Heap[0].Count < count {
			topkheap.Heap[0].Count = count
			topkheap.Heap[0].Key = key
			topkheap.updateOrderDown(0)
		}
	}
}

func (topkheap *TopKHeap) updateOrder(i int) {
	if !topkheap.updateOrderDown(i) {
		topkheap.updateOrderUp(i)
	}
}

func (topkheap *TopKHeap) updateOrderDown(i int) bool {
	n := len(topkheap.Heap)
	i0 := i
	var (
		l, r, smallest int = 0, 0, 0
	)
	for i < n {
		l = topkheap.leftChild(i)
		r = topkheap.rightChild(i)
		smallest = i

		if l < n && topkheap.Heap[smallest].Count > topkheap.Heap[l].Count {
			smallest = l
		}
		if r < n && topkheap.Heap[smallest].Count > topkheap.Heap[r].Count {
			smallest = r
		}

		if smallest != i {
			topkheap.swap(smallest, i)
		} else {
			break
		}
		i = smallest
	}
	return i > i0
}

func (topkheap *TopKHeap) updateOrderUp(i int) {
	var par int = 0
	for i > 0 {
		par = topkheap.parent(i)
		if topkheap.Heap[par].Count > topkheap.Heap[i].Count {
			topkheap.swap(par, i)
			i = par
		} else {
			break
		}
	}
}
