// Package univmon implements UnivMon (Liu et al., SIGCOMM 2016): a pyramid of
// layers, each a CountL2HH plus a heap of its heaviest keys, from which L1, L2,
// entropy and cardinality are estimated.
package univmon

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// MaxLayerSize is the deepest pyramid: the layer finder and the query
// recurrences shift a 64-bit key hash right by up to layerSize-1.
const MaxLayerSize = 64

// bottomLayerFinder is the seed index of the hash that picks a key's deepest
// layer.
const bottomLayerFinder = 19

// UpdateMode records which update a pyramid has taken; the first update picks
// it, and it selects the query recurrence.
type UpdateMode uint8

const (
	// UpdateModeUnset is the mode of a pyramid no update has reached.
	UpdateModeUnset UpdateMode = iota
	// UpdateModeStandard updates every layer from 0 to the key's bottom layer.
	UpdateModeStandard
	// UpdateModeTerminal updates only the key's bottom layer.
	UpdateModeTerminal
)

// UnivMon is a pyramid of layerSize layers, each a sketchRow x sketchCol
// CountL2HH hashing at its layer index and a heap of at most heapSize keys of
// type K.
type UnivMon[K asapv1.HeapKey] struct {
	heapSize, sketchRow, sketchCol, layerSize int

	layers            []*CountL2HH
	heaps             []*hhHeap[K]
	bucketSize        uint64
	updateMode        UpdateMode
	candidateComplete []bool
}

func checkShape(heapSize, sketchRow, sketchCol, layerSize int) error {
	if heapSize <= 0 || layerSize <= 0 {
		return fmt.Errorf("univmon: heap size %d and layer count %d must be positive", heapSize, layerSize)
	}
	if layerSize > MaxLayerSize {
		return fmt.Errorf("univmon: layer count %d exceeds %d", layerSize, MaxLayerSize)
	}
	if uint64(heapSize) > math.MaxUint32 {
		return fmt.Errorf("univmon: heap size %d exceeds u32", heapSize)
	}
	return checkDimensions(sketchRow, sketchCol)
}

// NewUnivMon returns an empty pyramid of layerSize layers.
func NewUnivMon[K asapv1.HeapKey](heapSize, sketchRow, sketchCol, layerSize int) (*UnivMon[K], error) {
	if err := checkShape(heapSize, sketchRow, sketchCol, layerSize); err != nil {
		return nil, err
	}
	u := &UnivMon[K]{
		heapSize: heapSize, sketchRow: sketchRow, sketchCol: sketchCol, layerSize: layerSize,
		layers:            make([]*CountL2HH, layerSize),
		heaps:             make([]*hhHeap[K], layerSize),
		candidateComplete: make([]bool, layerSize),
	}
	for i := range layerSize {
		u.layers[i], _ = newCountL2HH(sketchRow, sketchCol, i)
		u.heaps[i] = newHHHeap[K](heapSize, heapSize)
		u.candidateComplete[i] = true
	}
	return u, nil
}

// HeapSize returns each layer's heap capacity.
func (u *UnivMon[K]) HeapSize() int { return u.heapSize }

// SketchRow returns each layer's row count.
func (u *UnivMon[K]) SketchRow() int { return u.sketchRow }

// SketchCol returns each layer's column count.
func (u *UnivMon[K]) SketchCol() int { return u.sketchCol }

// LayerSize returns the number of layers.
func (u *UnivMon[K]) LayerSize() int { return u.layerSize }

// BucketSize returns the total weight inserted.
func (u *UnivMon[K]) BucketSize() uint64 { return u.bucketSize }

// Mode returns the update mode.
func (u *UnivMon[K]) Mode() UpdateMode { return u.updateMode }

// CandidatesComplete reports, per layer, whether the heap still holds every
// key the layer received.
func (u *UnivMon[K]) CandidatesComplete() []bool { return slices.Clone(u.candidateComplete) }

// HeapEntries returns a copy of the layer's heap entries in descending count,
// then asapv1.CompareHeapKeys order.
func (u *UnivMon[K]) HeapEntries(layer int) []asapv1.HeapEntry[K] {
	es := make([]asapv1.HeapEntry[K], len(u.heaps[layer].entries))
	for i, e := range u.heaps[layer].entries {
		es[i] = asapv1.HeapEntry[K]{Key: cloneKey(e.Key), Count: e.Count}
	}
	asapv1.SortHeapEntries(es)
	return es
}

// LayerEstimate returns the layer's CountL2HH estimate for key.
func (u *UnivMon[K]) LayerEstimate(layer int, key K) float64 {
	return u.layers[layer].Estimate([]byte(keyID(key)))
}

// LayerL2 returns the layer's CountL2HH L2 estimate.
func (u *UnivMon[K]) LayerL2(layer int) float64 { return u.layers[layer].L2() }

// BottomLayerForHash returns the deepest layer a key whose bottom-layer hash
// is hash reaches: one less than the lowest l >= 1 with bit l clear.
func BottomLayerForHash(hash uint64, layerSize int) int {
	for l := 1; l < layerSize; l++ {
		if (hash>>l)&1 == 0 {
			return l - 1
		}
	}
	return layerSize - 1
}

func bottomLayerHash(id string) uint64 { return common.HashIt(bottomLayerFinder, []byte(id)) }

func (u *UnivMon[K]) begin(value int64, mode UpdateMode) error {
	if u.layerSize == 0 {
		return errors.New("univmon: the pyramid was not built by NewUnivMon")
	}
	if value < 0 {
		return fmt.Errorf("univmon: update weight %d is negative", value)
	}
	if u.updateMode != UpdateModeUnset && u.updateMode != mode {
		return errors.New("univmon: standard and terminal-only updates do not mix")
	}
	if u.bucketSize > math.MaxUint64-uint64(value) {
		return errors.New("univmon: total weight overflows u64")
	}
	u.updateMode = mode
	u.bucketSize += uint64(value)
	return nil
}

func (u *UnivMon[K]) updateLayer(i int, key K, id string, value int64) {
	l := u.layers[i]
	h := l.hash([]byte(id))
	l.insert(h, value)
	if !u.heaps[i].update(key, id, toInt64(l.estimate(h))) {
		u.candidateComplete[i] = false
	}
}

// Insert adds a non-negative weight for key to every layer from 0 to the
// key's bottom layer. It fails on a negative weight, on a pyramid FastInsert
// has updated, and when the total weight would overflow.
func (u *UnivMon[K]) Insert(key K, value int64) error {
	if err := u.begin(value, UpdateModeStandard); err != nil {
		return err
	}
	id := keyID(key)
	bottom := BottomLayerForHash(bottomLayerHash(id), u.layerSize)
	for i := 0; i <= bottom; i++ {
		u.updateLayer(i, key, id, value)
	}
	return nil
}

// FastInsert adds a non-negative weight for key to the key's bottom layer
// only. It fails on a negative weight, on a pyramid Insert has updated, and
// when the total weight would overflow.
func (u *UnivMon[K]) FastInsert(key K, value int64) error {
	if err := u.begin(value, UpdateModeTerminal); err != nil {
		return err
	}
	id := keyID(key)
	u.updateLayer(BottomLayerForHash(bottomLayerHash(id), u.layerSize), key, id, value)
	return nil
}

// toInt64 converts f to int64 truncating toward zero, saturating at the int64
// range, with NaN as 0.
func toInt64(f float64) int64 {
	switch {
	case f != f:
		return 0
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	default:
		return int64(f)
	}
}

func (u *UnivMon[K]) heavyThreshold(l2 float64, complete bool) int64 {
	if complete {
		return 0
	}
	return toInt64(l2 / math.Sqrt(float64(u.heapSize)))
}

// candidate is a key, by ID, and its frequency estimate.
type candidate struct {
	id    string
	count int64
}

// recurrence returns Y[0] of Y[last] = sum g(c), Y[i] = 2Y[i+1] + sum s(c)g(c),
// over each layer's candidates above its threshold, where s(c) is +1 or -1 by
// bit i+1 of the key's bottom-layer hash.
func (u *UnivMon[K]) recurrence(g func(float64) float64, candidates [][]candidate, thresholds []int64) float64 {
	last := u.layerSize - 1
	var y float64
	for i := last; i >= 0; i-- {
		var tmp float64
		for _, c := range candidates[i] {
			if c.count <= thresholds[i] {
				continue
			}
			if i == last {
				tmp += g(float64(c.count))
				continue
			}
			bit := (bottomLayerHash(c.id) >> (i + 1)) & 1
			tmp += (1 - 2*float64(bit)) * g(float64(c.count))
		}
		if i == last {
			y = tmp
		} else {
			y = 2*y + tmp
		}
	}
	return y
}

// CalcGSum estimates the sum of g over every key's frequency. A layer whose
// candidate set is incomplete counts only keys above l2/sqrt(heapSize): with
// isCard set in standard mode, and always in terminal mode.
func (u *UnivMon[K]) CalcGSum(g func(float64) float64, isCard bool) float64 {
	if u.bucketSize == 0 {
		return 0
	}
	if u.updateMode == UpdateModeTerminal {
		candidates, thresholds := u.terminalCandidates()
		return u.recurrence(g, candidates, thresholds)
	}
	candidates := make([][]candidate, u.layerSize)
	thresholds := make([]int64, u.layerSize)
	for i, h := range u.heaps {
		for _, id := range h.ids {
			candidates[i] = append(candidates[i], candidate{id, toInt64(u.layers[i].Estimate([]byte(id)))})
		}
		if isCard {
			thresholds[i] = u.heavyThreshold(u.layers[i].L2(), u.candidateComplete[i])
		}
	}
	return u.recurrence(g, candidates, thresholds)
}

// terminalCandidates rebuilds each layer's logical candidate set from the
// terminal strata at and below it, keeping the heapSize largest, with its
// threshold over the logical L2 of those strata.
func (u *UnivMon[K]) terminalCandidates() ([][]candidate, []int64) {
	logical := make([][]candidate, u.layerSize)
	thresholds := make([]int64, u.layerSize)
	cumulative := make(map[string]int64)
	suffixComplete := true
	var suffixL2 float64
	for level := u.layerSize - 1; level >= 0; level-- {
		suffixComplete = suffixComplete && u.candidateComplete[level]
		l2 := u.layers[level].L2()
		suffixL2 += l2 * l2
		for _, id := range u.heaps[level].ids {
			count := toInt64(u.layers[level].Estimate([]byte(id)))
			if old, ok := cumulative[id]; !ok || count > old {
				cumulative[id] = count
			}
		}
		retained := make([]candidate, 0, len(cumulative))
		for id, count := range cumulative {
			retained = append(retained, candidate{id, count})
		}
		slices.SortFunc(retained, func(a, b candidate) int {
			if c := cmp.Compare(b.count, a.count); c != 0 {
				return c
			}
			if c := cmp.Compare(bottomLayerHash(a.id), bottomLayerHash(b.id)); c != 0 {
				return c
			}
			return cmp.Compare(a.id, b.id)
		})
		complete := suffixComplete && len(retained) <= u.heapSize
		thresholds[level] = u.heavyThreshold(math.Sqrt(suffixL2), complete)
		retained = retained[:min(len(retained), u.heapSize)]
		cumulative = make(map[string]int64, len(retained))
		for _, c := range retained {
			cumulative[c.id] = c.count
		}
		logical[level] = retained
	}
	return logical, thresholds
}

// CalcL1 returns the total weight inserted.
func (u *UnivMon[K]) CalcL1() float64 { return float64(u.bucketSize) }

// CalcL2 returns the estimated L2 norm.
func (u *UnivMon[K]) CalcL2() float64 {
	return math.Sqrt(u.CalcGSum(func(x float64) float64 { return x * x }, false))
}

// CalcEntropy returns the estimated Shannon entropy, in bits.
func (u *UnivMon[K]) CalcEntropy() float64 {
	if u.bucketSize == 0 {
		return 0
	}
	sum := u.CalcGSum(func(x float64) float64 {
		if x > 0 {
			return x * math.Log2(x)
		}
		return 0
	}, false)
	total := float64(u.bucketSize)
	return math.Log2(total) - sum/total
}

// CalcCard returns the estimated number of distinct keys.
func (u *UnivMon[K]) CalcCard() float64 {
	return u.CalcGSum(func(float64) float64 { return 1 }, true)
}

// Merge adds o into u: the counters, then each layer's heap rebuilt from both
// heaps' keys at their merged estimates. It fails unless the shapes match and
// the update modes agree or one is unset.
func (u *UnivMon[K]) Merge(o *UnivMon[K]) error {
	if u.layerSize != o.layerSize || u.sketchRow != o.sketchRow || u.sketchCol != o.sketchCol || u.heapSize != o.heapSize {
		return fmt.Errorf("univmon: merging %d layers of %dx%d, heap %d, with %d layers of %dx%d, heap %d",
			u.layerSize, u.sketchRow, u.sketchCol, u.heapSize, o.layerSize, o.sketchRow, o.sketchCol, o.heapSize)
	}
	mode := u.updateMode
	switch {
	case mode == UpdateModeUnset:
		mode = o.updateMode
	case o.updateMode != UpdateModeUnset && o.updateMode != mode:
		return errors.New("univmon: standard and terminal-only pyramids do not merge")
	}
	if u.bucketSize > math.MaxUint64-o.bucketSize {
		return errors.New("univmon: merged total weight overflows u64")
	}
	u.updateMode = mode
	u.bucketSize += o.bucketSize
	for i := range u.layerSize {
		var keys []K
		var ids []string
		seen := make(map[string]bool)
		for _, h := range []*hhHeap[K]{u.heaps[i], o.heaps[i]} {
			for j, id := range h.ids {
				if !seen[id] {
					seen[id] = true
					keys = append(keys, h.entries[j].Key)
					ids = append(ids, id)
				}
			}
		}
		complete := u.candidateComplete[i] && o.candidateComplete[i] && len(ids) <= u.heapSize
		u.layers[i].merge(o.layers[i])
		u.heaps[i].clear()
		for j, id := range ids {
			u.heaps[i].update(keys[j], id, toInt64(u.layers[i].Estimate([]byte(id))))
		}
		u.candidateComplete[i] = complete
	}
	return nil
}

// Free resets u to an empty pyramid of the same shape.
func (u *UnivMon[K]) Free() {
	u.bucketSize = 0
	u.updateMode = UpdateModeUnset
	for i := range u.layerSize {
		u.layers[i].clear()
		u.heaps[i].clear()
		u.candidateComplete[i] = true
	}
}
