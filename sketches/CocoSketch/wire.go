package cocosketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// maxWireRows is the number of rows the standard seed list can seed.
const maxWireRows = 20

func checkWireDims(rows, cols uint64) error {
	if rows == 0 || cols == 0 {
		return fmt.Errorf("cocosketch: ASAPv1 dimensions must be non-zero: rows=%d, cols=%d", rows, cols)
	}
	if rows > maxWireRows {
		return fmt.Errorf("cocosketch: ASAPv1 carries at most %d rows, got %d", maxWireRows, rows)
	}
	if cols > math.MaxUint32 {
		return fmt.Errorf("cocosketch: ASAPv1 cols %d exceeds u32", cols)
	}
	return nil
}

// checkBuckets rejects a table no insert could produce: mass in an empty
// bucket, a key outside the column its row hashes it to, or a key held twice.
func (c *CocoSketch) checkBuckets() error {
	seen := make(map[string]struct{})
	for r := range c.d {
		for col, b := range c.table[r] {
			if !b.HasKey {
				if b.Val != 0 {
					return fmt.Errorf("cocosketch: bucket (%d, %d) is unoccupied but carries value %d", r, col, b.Val)
				}
				continue
			}
			if m := c.hashIndex(r, b.Key); m != col {
				return fmt.Errorf("cocosketch: bucket (%d, %d) holds a key that maps to column %d", r, col, m)
			}
			if _, dup := seen[b.Key]; dup {
				return fmt.Errorf("cocosketch: bucket (%d, %d) repeats a key already stored elsewhere in the table", r, col)
			}
			seen[b.Key] = struct{}{}
		}
	}
	return nil
}

// MarshalASAPv1 encodes the bucket table as an ASAPv1 Coco envelope.
func (c *CocoSketch) MarshalASAPv1() ([]byte, error) {
	if c.d < 0 || c.length < 0 {
		return nil, fmt.Errorf("cocosketch: negative dimensions %dx%d", c.d, c.length)
	}
	if err := checkWireDims(uint64(c.d), uint64(c.length)); err != nil {
		return nil, err
	}
	if len(c.table) != c.d {
		return nil, fmt.Errorf("cocosketch: %d table rows for %d rows", len(c.table), c.d)
	}
	for r, row := range c.table {
		if len(row) != c.length {
			return nil, fmt.Errorf("cocosketch: table row %d has %d cols, want %d", r, len(row), c.length)
		}
	}
	if err := c.checkBuckets(); err != nil {
		return nil, err
	}
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("rows", uint64(c.d))
	md.Uint("cols", uint64(c.length))
	p := asapv1.NewEncoder()
	p.Array(2)
	p.Array(c.d * c.length)
	for _, row := range c.table {
		for _, b := range row {
			if b.HasKey {
				p.Str(b.Key)
			} else {
				p.Nil()
			}
		}
	}
	p.Array(c.d * c.length)
	for _, row := range c.table {
		for _, b := range row {
			p.Uint(b.Val)
		}
	}
	return asapv1.Marshal(asapv1.KindCoco, md, p)
}

// UnmarshalASAPv1 replaces c with the sketch in an ASAPv1 Coco envelope. The
// decoded sketch draws from a time-seeded generator.
func (c *CocoSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindCoco)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	rows := md.Uint32("rows")
	cols := md.Uint32("cols")
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkWireDims(uint64(rows), uint64(cols)); err != nil {
		return err
	}
	n := int(rows) * int(cols)
	p.ExpectArray(2)
	if got := p.Array(); p.Err() == nil && got != n {
		return fmt.Errorf("cocosketch: %d keys for %dx%d", got, rows, cols)
	}
	if err := p.Err(); err != nil {
		return err
	}
	buckets := make([]cocoBucket, n)
	for i := range buckets {
		if !p.Nil() {
			buckets[i] = cocoBucket{Key: p.Str(), HasKey: true}
		}
	}
	vals := asapv1.DecodeUints[uint64](p)
	if err := p.Finish(); err != nil {
		return err
	}
	if len(vals) != n {
		return fmt.Errorf("cocosketch: %d values for %dx%d", len(vals), rows, cols)
	}
	for i, v := range vals {
		buckets[i].Val = v
	}
	out, err := NewCocoSketch(int(rows), int(cols))
	if err != nil {
		return err
	}
	for r := range out.d {
		copy(out.table[r], buckets[r*out.length:(r+1)*out.length])
	}
	if err := out.checkBuckets(); err != nil {
		return err
	}
	*c = *out
	return nil
}
