package univmon

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
)

type UnivSketch struct {
	k           int // topK
	row         int
	col         int
	layer       int
	enableTopK  bool
	cs_layers   []*CountSketchUniv
	HH_layers   []*common.TopKHeap
	bucket_size int64
}

// NewUnivSketchPyramid creates a UnivSketch with pyramid structure (different sizes for Elephant vs Mice layers)
func NewUnivSketchPyramid(k, row, col, layer int) (us *UnivSketch, err error) {
	us = &UnivSketch{
		k:           k,
		row:         row,
		col:         col,
		layer:       layer,
		enableTopK:  true,
		bucket_size: 0,
	}

	us.cs_layers = make([]*CountSketchUniv, layer)
	us.HH_layers = make([]*common.TopKHeap, layer)

	// Initialize Layers
	if layer <= ELEPHANT_LAYER {
		for i := 0; i < layer; i++ {
			us.cs_layers[i], _ = NewCountSketchUniv(CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)
		}
		for i := 0; i < layer; i++ {
			us.HH_layers[i] = common.NewTopKHeap(k)
		}
	} else {
		for i := 0; i < ELEPHANT_LAYER; i++ {
			us.cs_layers[i], _ = NewCountSketchUniv(CS_ROW_NO_Univ_ELEPHANT, CS_COL_NO_Univ_ELEPHANT)
		}
		for i := 0; i < ELEPHANT_LAYER; i++ {
			us.HH_layers[i] = common.NewTopKHeap(TOPK_SIZE)
		}
		for i := ELEPHANT_LAYER; i < layer; i++ {
			us.cs_layers[i], _ = NewCountSketchUniv(CS_ROW_NO_Univ_MICE, CS_COL_NO_Univ_MICE)
		}
		for i := ELEPHANT_LAYER; i < layer; i++ {
			us.HH_layers[i] = common.NewTopKHeap(TOPK_SIZE_MICE)
		}
	}

	return us, nil
}

func (us *UnivSketch) Free() {
	us.bucket_size = 0
	for i := 0; i < us.layer; i++ {
		us.cs_layers[i].CleanCountSketchUniv()
		if us.HH_layers[i] != nil {
			us.HH_layers[i].Clean()
		}
	}
}

// --- Internal Helper ---

func findBottomLayerNum(hash uint64, layer int) int {
	if layer <= 1 {
		return 0
	}
	bitsToCheck := layer - 1 // bits [1..layer-1]
	if bitsToCheck < 63 {
		mask := (uint64(1) << bitsToCheck) - 1
		zeroBits := (^(hash >> 1)) & mask
		if zeroBits == 0 {
			return layer - 1
		}
		return bits.TrailingZeros64(zeroBits)
	}
	for l := 1; l < layer; l++ {
		if ((hash >> l) & 1) == 0 {
			return l - 1
		}
	}
	return layer - 1
}

// --- Main Update Logic ---

func (us *UnivSketch) updateLayersNoTopK(hash uint64, value int64, bottomLayer int) {
	us.cs_layers[0].UpdateWithHash(hash, value)
	for l := 1; l <= bottomLayer; l++ {
		us.cs_layers[l].UpdateWithHashNoL2(hash, value)
	}
}

func (us *UnivSketch) updateLayersWithTopK(hash uint64, value int64, bottomLayer int, key string) {
	median := us.cs_layers[0].UpdateAndEstimateHash(hash, value)
	us.HH_layers[0].Update(key, median)
	for l := 1; l <= bottomLayer; l++ {
		median = us.cs_layers[l].UpdateAndEstimateHashNoL2(hash, value)
		us.HH_layers[l].Update(key, median)
	}
}

func (us *UnivSketch) SetTopKEnabled(enabled bool) {
	us.enableTopK = enabled
}

// Update inserts a value into the sketch.
func (us *UnivSketch) Update(input *common.SketchInput, value int64) {
	if input == nil || value == 0 {
		return
	}
	us.bucket_size += value

	bottomLayer := findBottomLayerNum(input.Hash, us.layer)
	if !us.enableTopK {
		us.updateLayersNoTopK(input.Hash, value, bottomLayer)
		return
	}

	us.updateLayersWithTopK(input.Hash, value, bottomLayer, string(input.Bytes))
}

// UpdateWithHashOnly is a fast path for stream updates that do not require key-aware TopK tracking.
func (us *UnivSketch) UpdateWithHashOnly(hash uint64, value int64) {
	if value == 0 {
		return
	}
	us.bucket_size += value
	us.updateLayersNoTopK(hash, value, findBottomLayerNum(hash, us.layer))
}

// TypeName returns the sketch type name.
func (us *UnivSketch) TypeName() string {
	return "univmon"
}

// InsertWithHash implements common.Sketch.
func (us *UnivSketch) InsertWithHash(hash uint64) {
	us.UpdateWithHashOnly(hash, 1)
}

// QueryWithHash provides access to cardinality/sum estimates via the interface.
func (us *UnivSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryCardinality:
		return us.GetCardinality(), nil
	case common.QuerySum:
		return us.cs_layers[0].QueryWithHash(q, hash)
	case common.QueryFrequency:
		return us.cs_layers[0].QueryWithHash(q, hash)
	case common.QuerySum2: // FIX: ADD THIS CASE
		// Retrieve L2 from the first CountSketch layer
		return us.cs_layers[0].QueryWithHash(q, hash)
	default:
		return 0, common.ErrUnsupportedQuery
	}
}

// Merge combines another common.Sketch into this one.
func (us *UnivSketch) Merge(other common.Sketch) error {
	o, ok := other.(*UnivSketch)
	if !ok {
		return fmt.Errorf("cannot merge: incompatible sketch type (expected *UnivSketch)")
	}

	if us.layer != o.layer {
		return fmt.Errorf("univmon: layer mismatch (%d vs %d)", us.layer, o.layer)
	}

	us.bucket_size += o.bucket_size

	for i := 0; i < us.layer; i++ {
		// A. Merge CountSketch Layer
		// This now calls the FIXED CountSketchUniv.Merge which handles L2 correctly
		if err := us.cs_layers[i].Merge(o.cs_layers[i]); err != nil {
			return fmt.Errorf("error merging CS layer %d: %v", i, err)
		}

		// B. Merge TopK Heaps Manually
		if !us.enableTopK {
			continue
		}
		for _, item := range o.HH_layers[i].Heap {
			index, found := us.HH_layers[i].Find(item.Key)
			if found {
				currentCount := us.HH_layers[i].Heap[index].Count
				us.HH_layers[i].Update(item.Key, currentCount+item.Count)
			} else {
				us.HH_layers[i].Update(item.Key, item.Count)
			}
		}
	}

	return nil
}

// --- Merging Logic ---

// // MergeWith combines another UnivSketch into this one.
// func (us *UnivSketch) MergeWith(other *UnivSketch) {
// 	if us.layer != other.layer {
// 		// Ideally return error, but signature follows legacy void style
// 		fmt.Println("Error: UnivSketch layer mismatch in MergeWith")
// 		return
// 	}

// 	us.bucket_size += other.bucket_size

// 	for i := 0; i < us.layer; i++ {
// 		// 1. Merge CountSketch layers (Sum counters)
// 		// We can cast to common.Sketch or call Merge directly if accessible
// 		err := us.cs_layers[i].Merge(other.cs_layers[i])
// 		if err != nil {
// 			fmt.Println("Error merging CS layer:", err)
// 		}

// 		// 2. Merge TopK Heaps
// 		// Since common.TopKHeap doesn't have a "Sum-Merge", we implement it manually here.
// 		// We create a new temporary heap to consolidate counts.
// 		newHeap := common.NewTopKHeap(us.HH_layers[i].K) // Assuming K is accessible/same

// 		// Add items from this sketch
// 		for _, item := range us.HH_layers[i].Heap {
// 			newHeap.Update(item.Key, item.Count)
// 		}

// 		// Add items from other sketch (Summing counts if key exists)
// 		for _, item := range other.HH_layers[i].Heap {
// 			index, found := newHeap.Find(item.Key)
// 			if found {
// 				// Sum existing count with new count
// 				currentCount := newHeap.Heap[index].Count
// 				newHeap.Update(item.Key, currentCount+item.Count)
// 			} else {
// 				// Insert new item
// 				newHeap.Update(item.Key, item.Count)
// 			}
// 		}

// 		// Replace the heap for this layer
// 		us.HH_layers[i] = newHeap
// 	}
// }

// --- Query Functions ---

func (us *UnivSketch) calcGSumHeuristic(g func(float64) float64, isCard bool) float64 {
	Y := make([]float64, us.layer)
	var coe float64 = 1
	var tmp float64 = 0

	Y[us.layer-1] = 0
	l2_val, _ := us.cs_layers[us.layer-1].QueryWithHash(common.QuerySum2, 0)
	var threshold int64 = int64(l2_val * 0.01)
	if !isCard {
		threshold = 0
	}

	for _, item := range us.HH_layers[us.layer-1].Heap {
		if item.Count > threshold {
			tmp += g(float64(item.Count))
		}
	}
	Y[us.layer-1] = tmp

	for i := (us.layer - 2); i >= 0; i-- {
		tmp = 0
		l2_val, _ = us.cs_layers[i].QueryWithHash(common.QuerySum2, 0)
		threshold = int64(l2_val * 0.01)
		if !isCard {
			threshold = 0
		}

		for _, item := range us.HH_layers[i].Heap {
			if item.Count > threshold {
				h := common.Hash64([]byte(item.Key))
				bit := (h >> (i + 1)) & 1
				coe = 1 - 2*float64(bit)
				tmp += coe * g(float64(item.Count))
			}
		}
		Y[i] = 2*Y[i+1] + tmp
	}

	return Y[0]
}

func (us *UnivSketch) GetEntropy() float64 {
	if us.bucket_size == 0 {
		return 0
	}
	tmp := us.calcGSumHeuristic(func(x float64) float64 {
		if x > 0 {
			return x * math.Log2(x)
		}
		return 0
	}, false)
	return math.Log2(float64(us.bucket_size)) - tmp/float64(us.bucket_size)
}

func (us *UnivSketch) GetCardinality() float64 {
	return us.calcGSumHeuristic(func(x float64) float64 { return 1 }, true)
}

func (us *UnivSketch) QueryTopK(K int) *common.TopKHeap {
	topk := common.NewTopKHeap(K)

	for i := (us.layer - 1); i >= 0; i-- {
		l2_val, _ := us.cs_layers[i].QueryWithHash(common.QuerySum2, 0)
		var threshold int64 = int64(l2_val * 0.01)

		for _, item := range us.HH_layers[i].Heap {
			if item.Count > threshold {
				topk.Update(item.Key, item.Count)
			}
		}
	}
	return topk
}

func (us *UnivSketch) GetMemoryKB() float64 {
	var total_topk float64 = 0
	for i := 0; i < us.layer; i++ {
		total_topk += us.HH_layers[i].GetMemoryBytes()
	}
	csSize := float64(CS_COL_NO_Univ_ELEPHANT) * float64(CS_ROW_NO_Univ_ELEPHANT) * float64(us.layer) * 8
	return (csSize + total_topk) / 1024
}

type topKHeapSnapshot struct {
	Heap []common.Item
	K    int
}

type univSketchSnapshot struct {
	K          int
	Row        int
	Col        int
	Layer      int
	BucketSize int64
	CSLayers   [][]byte
	HHLayers   []topKHeapSnapshot
}

// SerializeToBytes serializes UnivSketch into bytes.
func (us *UnivSketch) SerializeToBytes() ([]byte, error) {
	csLayers := make([][]byte, len(us.cs_layers))
	for i, cs := range us.cs_layers {
		b, err := cs.SerializeToBytes()
		if err != nil {
			return nil, err
		}
		csLayers[i] = b
	}

	hhLayers := make([]topKHeapSnapshot, len(us.HH_layers))
	for i, hh := range us.HH_layers {
		if hh == nil {
			continue
		}
		heapCp := append([]common.Item(nil), hh.Heap...)
		hhLayers[i] = topKHeapSnapshot{
			Heap: heapCp,
			K:    hh.K,
		}
	}

	return common.EncodeToBytes(univSketchSnapshot{
		K:          us.k,
		Row:        us.row,
		Col:        us.col,
		Layer:      us.layer,
		BucketSize: us.bucket_size,
		CSLayers:   csLayers,
		HHLayers:   hhLayers,
	})
}

// DeserializeUnivSketchFromBytes restores UnivSketch from serialized bytes.
func DeserializeUnivSketchFromBytes(data []byte) (*UnivSketch, error) {
	var snap univSketchSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.Layer <= 0 {
		return nil, errors.New("invalid snapshot: layer must be positive")
	}
	if len(snap.CSLayers) != snap.Layer || len(snap.HHLayers) != snap.Layer {
		return nil, errors.New("invalid snapshot: layer payload mismatch")
	}

	us := &UnivSketch{
		k:           snap.K,
		row:         snap.Row,
		col:         snap.Col,
		layer:       snap.Layer,
		enableTopK:  true,
		bucket_size: snap.BucketSize,
		cs_layers:   make([]*CountSketchUniv, snap.Layer),
		HH_layers:   make([]*common.TopKHeap, snap.Layer),
	}

	for i := 0; i < snap.Layer; i++ {
		cs, err := DeserializeCountSketchUnivFromBytes(snap.CSLayers[i])
		if err != nil {
			return nil, err
		}
		us.cs_layers[i] = cs

		hh := common.NewTopKHeap(snap.HHLayers[i].K)
		hh.Heap = append([]common.Item(nil), snap.HHLayers[i].Heap...)
		hh.RecomputeMemory()
		us.HH_layers[i] = hh
	}

	return us, nil
}
