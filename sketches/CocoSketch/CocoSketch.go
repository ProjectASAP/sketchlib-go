package cocosketch

import (
	"errors"
	"math/rand"
	"strconv"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

type cocoBucket struct {
	Key    string
	Val    uint64
	HasKey bool
}

type CocoSketch struct {
	d      int
	length int

	tableStore *storage.Vector2D[cocoBucket]
	table      [][]cocoBucket

	rng *rand.Rand
}

func NewCocoSketch(d, length int) (*CocoSketch, error) {

	if d <= 0 || length <= 0 {
		return nil, errors.New("d and length must be > 0")
	}

	tableStore, err := storage.Vector2DFromFn[cocoBucket](
		d, length,
		func(_, _ int) cocoBucket { return cocoBucket{} },
	)

	if err != nil {
		return nil, err
	}

	src := rand.NewSource(time.Now().UnixNano())

	return &CocoSketch{
		d:          d,
		length:     length,
		tableStore: tableStore,
		table:      tableStore.As2D(),
		rng:        rand.New(src),
	}, nil
}

func (c *CocoSketch) SetSeed(seed int64) {
	c.rng = rand.New(rand.NewSource(seed))
}

func (c *CocoSketch) TypeName() string {
	return "CocoSketch"
}

// hashIndex is key's column in row: its xxh3 hash under seed list entry row,
// modulo length.
func (c *CocoSketch) hashIndex(row int, key string) int {
	return int(common.HashIt(row, []byte(key)) % uint64(c.length))
}

// hashKey is the key under which the hash-only methods count hash.
func hashKey(hash uint64) string {
	return strconv.FormatUint(hash, 16)
}

// insertKeyValue adds v to key's bucket if one of its d buckets holds it, else
// claims the first free one, else adds v to the smallest and elects key with
// probability v/val.
func (c *CocoSketch) insertKeyValue(key string, v uint64) {

	minRow := c.d
	minVal := ^uint64(0)
	var free *cocoBucket

	for i := 0; i < c.d; i++ {

		idx := c.hashIndex(i, key)
		b := &c.table[i][idx]

		if b.HasKey {

			if b.Key == key {
				b.Val += v
				return
			}

			if b.Val < minVal {
				minVal = b.Val
				minRow = i
			}

		} else if free == nil {
			free = b
		}
	}

	if free != nil {
		free.Key = key
		free.Val = v
		free.HasKey = true
		return
	}

	if minRow >= c.d {
		minRow = 0
	}

	idx := c.hashIndex(minRow, key)
	b := &c.table[minRow][idx]

	b.Val += v

	if float64(v)/float64(b.Val) > c.rng.Float64() {
		b.Key = key
	}
}

// InsertWithHash counts one occurrence of the key hashKey(hash).
func (c *CocoSketch) InsertWithHash(hash uint64) {
	c.insertKeyValue(hashKey(hash), 1)
}

func (c *CocoSketch) Insert(key string, v uint64) {

	if key == "" || v == 0 {
		return
	}

	c.insertKeyValue(key, v)
}

// EstimateHash estimates the key hashKey(hash).
func (c *CocoSketch) EstimateHash(hash uint64) uint64 {
	return c.Estimate(hashKey(hash))
}

// Estimate sums the buckets key maps to that hold key.
func (c *CocoSketch) Estimate(key string) uint64 {

	total := uint64(0)

	for i := 0; i < c.d; i++ {

		idx := c.hashIndex(i, key)
		b := c.table[i][idx]

		if b.HasKey && b.Key == key {
			total += b.Val
		}
	}

	return total
}

func (c *CocoSketch) EstimateWithUDF(partialKey string, udf func(full, partial string) bool) uint64 {

	total := uint64(0)

	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {

			b := c.table[i][j]

			if !b.HasKey {
				continue
			}

			if udf(b.Key, partialKey) {
				total += b.Val
			}
		}
	}

	return total
}

func (c *CocoSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {

	if q == common.QuerySum2 {
		return 0, nil
	}

	if q != common.QueryFrequency {
		return 0, common.ErrUnsupportedQuery
	}

	return float64(c.EstimateHash(hash)), nil
}

func (c *CocoSketch) Merge(other common.Sketch) error {

	o, ok := other.(*CocoSketch)

	if !ok {
		return errors.New("cannot merge different sketch types")
	}

	if c.d != o.d || c.length != o.length {
		return errors.New("incompatible sketches")
	}

	for i := 0; i < o.d; i++ {
		for j := 0; j < o.length; j++ {

			b := o.table[i][j]

			if b.HasKey {
				c.insertKeyValue(b.Key, b.Val)
			}
		}
	}

	return nil
}

func (c *CocoSketch) Clear() {

	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			c.table[i][j] = cocoBucket{}
		}
	}
}
