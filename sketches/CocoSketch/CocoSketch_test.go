package cocosketch

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCocoFailsOnInvalidParams(t *testing.T) {
	_, err := NewCoco[string](0, 8, FNV1a64String)
	require.Error(t, err)

	_, err = NewCoco[string](3, 0, FNV1a64String)
	require.Error(t, err)

	_, err = NewCoco[string](1, 4, nil)
	require.Error(t, err)
}

func TestCocoInsertAndTable(t *testing.T) {
	hashes := map[string]uint64{
		"apple":  composeTestHash(0, 0),
		"banana": composeTestHash(1, 2),
	}

	c, err := NewCoco[string](3, 5, stubStringHasher(hashes))
	require.NoError(t, err)
	c.rng = rand.New(rand.NewSource(1))

	for i := 0; i < 3; i++ {
		c.Insert("apple")
	}
	c.Insert("banana")

	applePos := c.positions(hashes["apple"])
	foundApple := false
	for i := 0; i < c.d; i++ {
		if c.keys[i][applePos[i]] == "apple" {
			require.Equal(t, uint64(3), c.counts[i][applePos[i]], "apple count mismatch at array %d position %d", i, applePos[i])
			foundApple = true
		}
	}
	require.True(t, foundApple, "apple not stored in any candidate bucket")

	table := c.Table(AggregateSum)
	require.Equal(t, uint64(3), table["apple"])
	require.Equal(t, uint64(1), table["banana"])
}

func TestCocoMerge(t *testing.T) {
	hashes := map[string]uint64{
		"apple": composeTestHash(0, 0),
	}

	base, err := NewCoco[string](2, 4, stubStringHasher(hashes))
	require.NoError(t, err)
	base.rng = rand.New(rand.NewSource(1))

	other, err := NewCoco[string](2, 4, stubStringHasher(hashes))
	require.NoError(t, err)
	other.rng = rand.New(rand.NewSource(2))

	other.Insert("apple")
	other.Insert("apple")

	err = base.Merge(other)
	require.NoError(t, err)

	applePos := base.positions(hashes["apple"])
	found := false
	for i := 0; i < base.d; i++ {
		if base.keys[i][applePos[i]] == "apple" {
			require.Equal(t, uint64(2), base.counts[i][applePos[i]])
			found = true
		}
	}
	require.True(t, found, "apple not merged into expected buckets")

	err = base.Merge(nil)
	require.NoError(t, err)

	mismatch, err := NewCoco[string](3, 4, stubStringHasher(hashes))
	require.NoError(t, err)
	require.Error(t, base.Merge(mismatch))
}

func TestCocoMergeEntryWithPositions(t *testing.T) {
	c, err := NewCoco[string](2, 5, stubStringHasher(map[string]uint64{"grape": composeTestHash(0, 0)}))
	require.NoError(t, err)
	c.rng = rand.New(rand.NewSource(3))

	entry := Entry[string]{
		Key:            "grape",
		Value:          5,
		Positions:      []int{-1, 6},
		PositionsValid: true,
	}
	require.NoError(t, c.MergeEntry(entry))

	require.Equal(t, "grape", c.keys[0][4])
	require.Equal(t, uint64(5), c.counts[0][4])

	entry.Value = 3
	require.NoError(t, c.MergeEntry(entry))
	require.Equal(t, uint64(8), c.counts[0][4])

	bad := Entry[string]{
		Key:            "bad",
		Value:          1,
		Positions:      []int{1},
		PositionsValid: true,
	}
	require.Error(t, c.MergeEntry(bad))
}

func TestCocoSnapshotAndAggregations(t *testing.T) {
	c, err := NewCoco[string](3, 4, stubStringHasher(map[string]uint64{"apple": composeTestHash(0, 0), "pear": composeTestHash(1, 2)}))
	require.NoError(t, err)

	c.keys[0][0] = "apple"
	c.counts[0][0] = 2
	c.keys[1][0] = "apple"
	c.counts[1][0] = 4
	c.keys[2][0] = "apple"
	c.counts[2][0] = 6

	c.keys[0][1] = "pear"
	c.counts[0][1] = 5
	c.keys[1][1] = "pear"
	c.counts[1][1] = 5

	snapshot := c.Snapshot()
	require.Len(t, snapshot["apple"], 3)
	require.Len(t, snapshot["pear"], 2)

	sum := c.Table(AggregateSum)
	require.Equal(t, uint64(12), sum["apple"])
	require.Equal(t, uint64(10), sum["pear"])

	median := c.Table(AggregateMedian)
	require.Equal(t, uint64(4), median["apple"])
	require.Equal(t, uint64(5), median["pear"])

	raw := c.Table(AggregateRaw)
	require.Equal(t, uint64(2), raw["apple"])
	require.Equal(t, uint64(5), raw["pear"])
}

func TestCocoEstimatedMemoryAndThreshold(t *testing.T) {
	intHasher := func(v int) uint64 {
		return uint64(v)
	}

	c, err := NewCoco[int](3, 7, intHasher)
	require.NoError(t, err)
	require.Equal(t, 3*7*8, c.EstimatedMemoryBytes())

	require.Equal(t, uint64(100), HeavyThreshold(1000, 0.1))
	require.Equal(t, ^uint64(0), HeavyThreshold(10, 0))
	require.Equal(t, uint64(1), HeavyThreshold(5, 0.01))
}

func composeTestHash(low, high uint32) uint64 {
	return uint64(high)<<32 | uint64(low)
}

func stubStringHasher(mapping map[string]uint64) Hasher[string] {
	return func(k string) uint64 {
		v, ok := mapping[k]
		if !ok {
			panic("unexpected key: " + k)
		}
		return v
	}
}
