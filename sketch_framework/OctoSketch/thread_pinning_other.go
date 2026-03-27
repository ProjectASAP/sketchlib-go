//go:build !linux

package octosketch

func setCurrentThreadAffinity(coreID int) {}
