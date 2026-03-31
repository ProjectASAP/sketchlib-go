package octosketch

import "runtime"

func ensureDedicatedThreads(count int) {
	if count <= 0 {
		return
	}
	cur := runtime.GOMAXPROCS(0)
	if cur < count {
		runtime.GOMAXPROCS(count)
	}
}

func lockThreadToCore(coreID int) func() {
	runtime.LockOSThread()
	setCurrentThreadAffinity(coreID)
	return runtime.UnlockOSThread
}
