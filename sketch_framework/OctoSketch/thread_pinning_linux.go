//go:build linux

package octosketch

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func setCurrentThreadAffinity(coreID int) {
	if coreID < 0 {
		return
	}
	ncpu := runtime.NumCPU()
	if ncpu <= 0 {
		return
	}
	coreID %= ncpu
	var set unix.CPUSet
	set.Zero()
	set.Set(coreID)
	_ = unix.SchedSetaffinity(0, &set)
}
