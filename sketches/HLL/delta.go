package hll

// RegisterUpdate holds a single register index whose value increased.
type RegisterUpdate struct {
	Index uint32
	Value uint8
}

// RegisterDelta is the native Go representation of a sparse HLL register delta.
// No proto dependency.
type RegisterDelta struct {
	Updates []RegisterUpdate
}

// ComputeRegisterDelta computes the set of registers that increased from
// snapshot to current. HLL uses max semantics so only increases are meaningful;
// decreases cannot occur at fixed precision.
func ComputeRegisterDelta(snapshot, current *HyperLogLog) *RegisterDelta {
	snapRegs := snapshot.RegisterSlice()
	currRegs := current.RegisterSlice()

	n := len(currRegs)
	if len(snapRegs) < n {
		n = len(snapRegs)
	}

	d := &RegisterDelta{
		Updates: make([]RegisterUpdate, 0, n/100), // ~1% change rate hint
	}
	for i := 0; i < n; i++ {
		if currRegs[i] > snapRegs[i] {
			d.Updates = append(d.Updates, RegisterUpdate{Index: uint32(i), Value: currRegs[i]})
		}
	}
	// Guard: if current has more registers than snapshot (shouldn't happen at
	// fixed precision), include all non-zero extras.
	for i := n; i < len(currRegs); i++ {
		if currRegs[i] > 0 {
			d.Updates = append(d.Updates, RegisterUpdate{Index: uint32(i), Value: currRegs[i]})
		}
	}
	return d
}

// ApplyRegisterDelta applies d to target using max semantics.
// Each update sets target[index] = max(target[index], value).
func ApplyRegisterDelta(target *HyperLogLog, d *RegisterDelta) {
	regs := target.Registers.AsMutSlice()
	for i := range d.Updates {
		u := &d.Updates[i]
		if int(u.Index) < len(regs) && u.Value > regs[u.Index] {
			regs[u.Index] = u.Value
		}
	}
}
