package foldcommon

type FoldEntry struct {
	FullCol uint32
	Count   float64
}

const (
	cellStateEmpty uint8 = iota
	cellStateSingle
	cellStateCollided
)

// FoldCell stores one or more logical columns that collide into the same
// folded physical cell.
type FoldCell struct {
	State   uint8
	FullCol uint32
	Count   float64
	Entries []FoldEntry
}

func (c *FoldCell) Insert(fullCol uint32, delta float64) {
	switch c.State {
	case cellStateEmpty:
		c.State = cellStateSingle
		c.FullCol = fullCol
		c.Count = delta
		c.Entries = nil
	case cellStateSingle:
		if c.FullCol == fullCol {
			c.Count += delta
			return
		}
		c.State = cellStateCollided
		c.Entries = []FoldEntry{
			{FullCol: c.FullCol, Count: c.Count},
			{FullCol: fullCol, Count: delta},
		}
		c.FullCol = 0
		c.Count = 0
	case cellStateCollided:
		for i := range c.Entries {
			if c.Entries[i].FullCol == fullCol {
				c.Entries[i].Count += delta
				return
			}
		}
		c.Entries = append(c.Entries, FoldEntry{FullCol: fullCol, Count: delta})
	}
}

func (c *FoldCell) Query(fullCol uint32) float64 {
	switch c.State {
	case cellStateEmpty:
		return 0
	case cellStateSingle:
		if c.FullCol == fullCol {
			return c.Count
		}
		return 0
	case cellStateCollided:
		for i := range c.Entries {
			if c.Entries[i].FullCol == fullCol {
				return c.Entries[i].Count
			}
		}
	}
	return 0
}

func (c *FoldCell) MergeFrom(other *FoldCell) {
	other.Visit(func(fullCol uint32, count float64) {
		c.Insert(fullCol, count)
	})
}

func (c *FoldCell) EntryCount() int {
	switch c.State {
	case cellStateEmpty:
		return 0
	case cellStateSingle:
		return 1
	case cellStateCollided:
		return len(c.Entries)
	default:
		return 0
	}
}

func (c *FoldCell) IsEmpty() bool {
	return c.State == cellStateEmpty
}

func (c *FoldCell) Clear() {
	c.State = cellStateEmpty
	c.FullCol = 0
	c.Count = 0
	c.Entries = nil
}

func (c *FoldCell) Visit(fn func(fullCol uint32, count float64)) {
	switch c.State {
	case cellStateSingle:
		fn(c.FullCol, c.Count)
	case cellStateCollided:
		for i := range c.Entries {
			entry := c.Entries[i]
			fn(entry.FullCol, entry.Count)
		}
	}
}
