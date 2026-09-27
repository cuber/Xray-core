package anytls_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
)

type nativeResourceSample struct {
	heap       uint64
	fds, tasks int
}

func sampleNativeResources(t *testing.T) nativeResourceSample {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return nativeResourceSample{heap: memory.HeapAlloc, fds: openFDCount(t), tasks: runtime.NumGoroutine()}
}

func TestAnyTLSOutboundNativeFaultResourceRounds(t *testing.T) {
	var baseline nativeResourceSample
	for round := range 32 {
		// Each subtest owns a real Core instance, TLS peers, gRPC manager,
		// pending FIN/Close barriers and a live replacement. Its cleanup has
		// completed before t.Run returns and the parent samples resources.
		if !t.Run(fmt.Sprintf("round-%02d", round), runNativeReturnClose) {
			return
		}
		current := sampleNativeResources(t)
		if round == 3 {
			baseline = current
		}
		if round < 3 {
			continue
		}
		t.Logf("round=%d retained_heap=%d/%d fd=%d/%d goroutines=%d/%d", round,
			current.heap, baseline.heap, current.fds, baseline.fds, current.tasks, baseline.tasks)
		// Exact fixture ownership is asserted inside each round. Allow bounded
		// runtime/cache variance here, not per-round growth or an endpoint retry.
		if current.heap > baseline.heap+8*1024*1024 || current.tasks > baseline.tasks+8 ||
			(baseline.fds >= 0 && current.fds > baseline.fds+4) {
			pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			path := ""
			profile, err := os.CreateTemp("", "anytls-native-resource-*.pprof")
			if err == nil {
				path = profile.Name()
				err = pprof.WriteHeapProfile(profile)
				profile.Close()
			}
			t.Fatalf("native post-cleanup growth: baseline=%+v current=%+v heap_profile=%s err=%v", baseline, current, path, err)
		}
	}
}
