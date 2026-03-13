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
	Hash   uint64
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

func (c *CocoSketch) hashIndex(row int, hash uint64) int {
	return int(common.DeriveIndex(hash, row, uint32(c.length)))
}

func (c *CocoSketch) insertKeyValue(hash uint64, v uint64) {

	minRow := c.d
	minVal := ^uint64(0)

	for i := 0; i < c.d; i++ {

		idx := c.hashIndex(i, hash)
		b := &c.table[i][idx]

		if b.HasKey {

			if b.Hash == hash {
				b.Val += v
				return
			}

			if b.Val < minVal {
				minVal = b.Val
				minRow = i
			}

		} else {

			b.Hash = hash
			b.Val = v
			b.HasKey = true
			return
		}
	}

	if minRow >= c.d {
		minRow = 0
	}

	idx := c.hashIndex(minRow, hash)
	b := &c.table[minRow][idx]

	b.Val += v

	if float64(v)/float64(b.Val) > c.rng.Float64() {
		b.Hash = hash
	}
}

func (c *CocoSketch) InsertWithHash(hash uint64) {
	c.insertKeyValue(hash, 1)
}

func (c *CocoSketch) Insert(key string, v uint64) {

	if key == "" || v == 0 {
		return
	}

	hash := common.Hash64([]byte(key))
	c.insertKeyValue(hash, v)
}

func (c *CocoSketch) EstimateHash(hash uint64) uint64 {

	total := uint64(0)

	for i := 0; i < c.d; i++ {

		idx := c.hashIndex(i, hash)
		b := c.table[i][idx]

		if b.HasKey && b.Hash == hash {
			total += b.Val
		}
	}

	return total
}

func (c *CocoSketch) Estimate(partialKey string) uint64 {

	hash := common.Hash64([]byte(partialKey))
	return c.EstimateHash(hash)
}

func (c *CocoSketch) EstimateWithUDF(partialKey string, udf func(full, partial string) bool) uint64 {

	total := uint64(0)

	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {

			b := c.table[i][j]

			if !b.HasKey {
				continue
			}

			keyStr := strconv.FormatUint(b.Hash, 16)

			if udf(keyStr, partialKey) {
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
				c.insertKeyValue(b.Hash, b.Val)
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

type cocoSnapshot struct {
	D     int
	W     int
	Table [][]cocoBucket
}

func (c *CocoSketch) SerializeToBytes() ([]byte, error) {

	table := make([][]cocoBucket, c.d)

	for i := 0; i < c.d; i++ {
		table[i] = append([]cocoBucket(nil), c.table[i]...)
	}

	return common.EncodeToBytes(cocoSnapshot{
		D:     c.d,
		W:     c.length,
		Table: table,
	})
}

func DeserializeCocoSketchFromBytes(data []byte) (*CocoSketch, error) {

	var snap cocoSnapshot

	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}

	if snap.D <= 0 || snap.W <= 0 {
		return nil, errors.New("invalid snapshot dimensions")
	}

	tableStore, err := storage.Vector2DFrom2D[cocoBucket](snap.Table)
	if err != nil {
		return nil, err
	}

	src := rand.NewSource(time.Now().UnixNano())

	return &CocoSketch{
		d:          snap.D,
		length:     snap.W,
		tableStore: tableStore,
		table:      tableStore.As2D(),
		rng:        rand.New(src),
	}, nil
}
