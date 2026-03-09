package storage

import (
	"testing"
	"time"
)

func traceTest(t *testing.T) {
	t.Helper()
	start := time.Now()
	t.Logf("[START] %s", t.Name())
	t.Cleanup(func() {
		t.Logf("[END] %s elapsed=%s", t.Name(), time.Since(start))
	})
}
