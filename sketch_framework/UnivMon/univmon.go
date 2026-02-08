package univmon

import (
	"fmt"
	"math"

	"github.com/approx-telemetry/sketchlib-go/common"
)

type UnivSketch struct {
	k           int // topK
	row         int
	col         int
	layer       int
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
		us.HH_layers[i].Clean()
	}
}

// --- Internal Helper ---

func findBottomLayerNum(hash uint64, layer int) int {
	// Optimization: check bit once per layer
	for l := 1; l < layer; l++ {
		if ((hash >> l) & 1) == 0 {
			return l - 1
		}
	}
	return layer - 1
}

// --- Main Update Logic ---

// Update inserts a value into the sketch.
func (us *UnivSketch) Update(input *common.SketchInput, value int64) {
	us.bucket_size += value

	bottom_layer_num := findBottomLayerNum(input.Hash, us.layer)
	keyStr := string(input.Bytes)

	// Determine optimization strategy based on depth
	if bottom_layer_num < ELEPHANT_LAYER {
		// Elephant Layers (Upper)
		for l := bottom_layer_num; l >= 0; l-- {
			var median_count int64
			if l == 0 {
				// Layer 0 requires full L2 update for accuracy
				median_count = us.cs_layers[l].UpdateAndEstimateHash(input.Hash, value)
			} else {
				// Optimization: Skip L2 update for intermediate layers
				median_count = us.cs_layers[l].UpdateAndEstimateHashNoL2(input.Hash, value)
			}
			us.HH_layers[l].Update(keyStr, median_count)
		}
	} else {
		// Mice Layers (Lower/Deep) + Elephant Layers

		// Update upper layers (Elephant)
		for l := ELEPHANT_LAYER - 1; l >= 0; l-- {
			var median_count int64
			if l == 0 {
				median_count = us.cs_layers[l].UpdateAndEstimateHash(input.Hash, value)
			} else {
				median_count = us.cs_layers[l].UpdateAndEstimateHashNoL2(input.Hash, value)
			}
			us.HH_layers[l].Update(keyStr, median_count)
		}

		// Update lower layers (Mice)
		for l := bottom_layer_num; l >= ELEPHANT_LAYER; l-- {
			median_count := us.cs_layers[l].UpdateAndEstimateHashNoL2(input.Hash, value)
			us.HH_layers[l].Update(keyStr, median_count)
		}
	}
}

// --- Legacy Wrappers for Compatibility ---

// univmon_processing is a wrapper for Update.
// Note: 'pos' and 'sign' arguments are ignored as the new framework derives indices from the hash.
func (us *UnivSketch) univmon_processing(key string, value int64, bottom_layer_num int, pos *([]int16), sign *([]int8)) {
	// Convert string key to common.SketchInput
	input := common.FromString(key)
	us.Update(input, value)
}

// univmon_processing_optimized is a wrapper for Update.
// The optimization logic (pyramid handling) is now built-in to Update.
func (us *UnivSketch) univmon_processing_optimized(key string, value int64, bottom_layer_num int, pos *([]int16), sign *([]int8)) {
	input := common.FromString(key)
	us.Update(input, value)
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

// TypeName returns the sketch type name.
func (us *UnivSketch) TypeName() string {
	return "univmon"
}

// InsertWithHash implements common.Sketch.
// Since UnivMon usually requires a string key for TopK, this is a simplified wrapper.
func (us *UnivSketch) InsertWithHash(hash uint64) {
	// We create a dummy input with the hash.
	// Note: The original key string is lost here, which affects TopK accuracy if strictly relying on this method.
	input := &common.SketchInput{
		Hash:  hash,
		Bytes: []byte{}, // Key is empty
	}
	us.Update(input, 1)
}

// QueryWithHash provides access to cardinality/sum estimates via the interface.
func (us *UnivSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryCardinality:
		return us.GetCardinality(), nil
	case common.QuerySum:
		// Return the estimate from the first layer (CountSketch)
		return us.cs_layers[0].QueryWithHash(q, hash)
	default:
		return 0, common.ErrUnsupportedQuery
	}
}

// Merge combines another common.Sketch into this one.
// Replaces the old MergeWith to satisfy the interface.
func (us *UnivSketch) Merge(other common.Sketch) error {
	// 1. Type Assertion: Ensure the other sketch is of the correct type
	o, ok := other.(*UnivSketch)
	if !ok {
		return fmt.Errorf("cannot merge: incompatible sketch type (expected *UnivSketch)")
	}

	// 2. Validation: Check if layers match
	if us.layer != o.layer {
		return fmt.Errorf("univmon: layer mismatch (%d vs %d)", us.layer, o.layer)
	}

	// 3. Update Metadata
	us.bucket_size += o.bucket_size

	// 4. Merge Layers
	for i := 0; i < us.layer; i++ {
		// A. Merge CountSketch Layer
		// Assuming cs_layers[i] already implements Merge(common.Sketch)
		if err := us.cs_layers[i].Merge(o.cs_layers[i]); err != nil {
			return fmt.Errorf("error merging CS layer %d: %v", i, err)
		}

		// B. Merge TopK Heaps Manually
		// (Heap library might not support direct merge, so we do it element-wise)
		for _, item := range o.HH_layers[i].Heap {
			index, found := us.HH_layers[i].Find(item.Key)
			if found {
				// Key exists: Sum the counts
				currentCount := us.HH_layers[i].Heap[index].Count
				us.HH_layers[i].Update(item.Key, currentCount+item.Count)
			} else {
				// New key: Insert it
				us.HH_layers[i].Update(item.Key, item.Count)
			}
		}
	}

	return nil
}

// --- Query Functions ---

func (us *UnivSketch) calcGSumHeuristic(g func(float64) float64, isCard bool) float64 {
	Y := make([]float64, us.layer)
	var coe float64 = 1
	var tmp float64 = 0

	// Bottom layer processing
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

	// Recursive estimator for upper layers
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

// QueryTopK returns the global Heavy Hitters list (Top-K items)
// by merging candidates from all layers that satisfy the L2 threshold.
func (us *UnivSketch) QueryTopK(K int) *common.TopKHeap {
	// Create new heap to hold final result
	topk := common.NewTopKHeap(K)

	// Iterate from bottom layer to top
	for i := (us.layer - 1); i >= 0; i-- {
		// Get L2 value (variance) from that layer to determine noise threshold
		l2_val, _ := us.cs_layers[i].QueryWithHash(common.QuerySum2, 0)

		// Threshold heuristic: item is considered significant if count > 1% of L2 norm
		var threshold int64 = int64(l2_val * 0.01)

		// Iterate all items in this layer's Heap
		for _, item := range us.HH_layers[i].Heap {
			// Only insert into final result if count exceeds noise threshold
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
	// Estimate: CS size + Heap size
	// Note: Accurate size depends on architecture (32/64 bit).
	// This is a rough estimation compatible with the legacy code logic.
	csSize := float64(CS_COL_NO_Univ_ELEPHANT) * float64(CS_ROW_NO_Univ_ELEPHANT) * float64(us.layer) * 8
	return (csSize + total_topk) / 1024
}
