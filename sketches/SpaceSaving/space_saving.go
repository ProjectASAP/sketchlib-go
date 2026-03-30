package spacesaving

// SpaceSaving implements the Weighted Space Saving algorithm
// (Metwally, Agrawal, Abbadi — "Efficient Computation of Frequent and
// Top-k Elements in Data Streams", ICDT 2005).
//
// It maintains a bounded table of at most k entries. Any item whose total
// weight exceeds W/k (W = sum of all weights inserted) is guaranteed to
// appear in the table.
//
// For unweighted streams (w=1) this is equivalent to Misra-Gries with the
// same guarantee: any item with frequency > N/k is tracked.
//
// Insert cost: O(log k) — one min-heap sift, no CS query needed.
// Candidates cost: O(k) — return all tracked key strings.
type SpaceSaving struct {
	k     int
	heap  []ssEntry          // binary min-heap ordered by count
	index map[string]int     // key → heap position for O(1) lookup
}

type ssEntry struct {
	key      string
	count    float64
	maxError float64 // max possible overcount: true_weight >= count - maxError
}

// NewSpaceSaving creates a new SpaceSaving tracker with capacity k.
func NewSpaceSaving(k int) *SpaceSaving {
	if k <= 0 {
		k = 1
	}
	return &SpaceSaving{
		k:     k,
		heap:  make([]ssEntry, 0, k),
		index: make(map[string]int, k),
	}
}

// Insert adds item key with weight w into the tracker.
func (ss *SpaceSaving) Insert(key string, w float64) {
	if w <= 0 {
		return
	}
	if idx, ok := ss.index[key]; ok {
		// Key already tracked: increment its count.
		ss.heap[idx].count += w
		ss.heapifyDown(idx) // count increased → may need to sink in min-heap
		return
	}
	if len(ss.heap) < ss.k {
		// Space available: add as new entry.
		idx := len(ss.heap)
		ss.heap = append(ss.heap, ssEntry{key: key, count: w})
		ss.index[key] = idx
		ss.heapifyUp(idx)
		return
	}
	// Table full: evict the minimum-count entry (root of min-heap).
	minCount := ss.heap[0].count
	delete(ss.index, ss.heap[0].key)
	ss.heap[0] = ssEntry{
		key:      key,
		count:    minCount + w,
		maxError: minCount,
	}
	ss.index[key] = 0
	ss.heapifyDown(0)
}

// Candidates returns the keys of all currently tracked items.
// The caller should query their actual counts from the sketch matrix.
func (ss *SpaceSaving) Candidates() []string {
	out := make([]string, len(ss.heap))
	for i, e := range ss.heap {
		out[i] = e.key
	}
	return out
}

// Len returns the number of currently tracked entries.
func (ss *SpaceSaving) Len() int { return len(ss.heap) }

// K returns the capacity of the tracker.
func (ss *SpaceSaving) K() int { return ss.k }

// Reset clears all tracked entries.
func (ss *SpaceSaving) Reset() {
	ss.heap = ss.heap[:0]
	ss.index = make(map[string]int, ss.k)
}

// GobEncode implements encoding.GobEncoder.
// SpaceSaving state is ephemeral (rebuilt on subsequent inserts), so we encode
// only the capacity k. The tracked entries are not persisted.
func (ss *SpaceSaving) GobEncode() ([]byte, error) {
	return []byte{byte(ss.k >> 24), byte(ss.k >> 16), byte(ss.k >> 8), byte(ss.k)}, nil
}

// GobDecode implements encoding.GobDecoder.
// Restores the capacity k and initialises an empty tracker.
func (ss *SpaceSaving) GobDecode(data []byte) error {
	if len(data) == 4 {
		ss.k = int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	}
	ss.heap = make([]ssEntry, 0, ss.k)
	ss.index = make(map[string]int, ss.k)
	return nil
}

// heapifyUp bubbles entry at index i up until the min-heap property holds.
func (ss *SpaceSaving) heapifyUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if ss.heap[p].count <= ss.heap[i].count {
			break
		}
		ss.swapAt(p, i)
		i = p
	}
}

// heapifyDown sinks entry at index i down until the min-heap property holds.
func (ss *SpaceSaving) heapifyDown(i int) {
	n := len(ss.heap)
	for {
		smallest := i
		l, r := 2*i+1, 2*i+2
		if l < n && ss.heap[l].count < ss.heap[smallest].count {
			smallest = l
		}
		if r < n && ss.heap[r].count < ss.heap[smallest].count {
			smallest = r
		}
		if smallest == i {
			break
		}
		ss.swapAt(i, smallest)
		i = smallest
	}
}

func (ss *SpaceSaving) swapAt(i, j int) {
	ss.heap[i], ss.heap[j] = ss.heap[j], ss.heap[i]
	ss.index[ss.heap[i].key] = i
	ss.index[ss.heap[j].key] = j
}
