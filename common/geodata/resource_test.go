package geodata

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/xtls/xray-core/common/geodata/strmatcher"
	"github.com/xtls/xray-core/common/utils"
)

func TestU012ProductionDATResources(t *testing.T) {
	t.Setenv("xray.location.asset", filepath.Join("..", "..", "resources"))
	for _, name := range []string{DefaultGeoSiteDat, DefaultGeoIPDat} {
		data, err := os.ReadFile(filepath.Join("..", "..", "resources", name))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s bytes=%d sha256=%x", name, len(data), sha256.Sum256(data))
	}
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%v", compact), func(t *testing.T) {
			cache := utils.NewWeakCacheMap[string, strmatcher.MphValueMatcher]()
			var factory DomainMatcherFactory = &MphDomainMatcherFactory{shared: cache}
			if compact {
				factory = &CompactMphDomainMatcherFactory{shared: cache}
			}
			runtime.GC()
			var baseline, held, released runtime.MemStats
			runtime.ReadMemStats(&baseline)
			// Sample construction separately from post-GC retention. This is a
			// sampled process heap high-water mark, not an exact allocator peak.
			stop, peakResult := make(chan struct{}), make(chan uint64, 1)
			go func() {
				peak := baseline.HeapAlloc
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var sample runtime.MemStats
					runtime.ReadMemStats(&sample)
					if sample.HeapAlloc > peak {
						peak = sample.HeapAlloc
					}
					select {
					case <-stop:
						peakResult <- peak
						return
					case <-ticker.C:
					}
				}
			}()
			m, err := factory.BuildMatcher([]*DomainRule{{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: DefaultGeoSiteDat, Code: "CN"}}}})
			close(stop)
			peak := <-peakResult
			if err != nil {
				t.Fatal(err)
			}
			var compiled []weak.Pointer[strmatcher.MphValueMatcher]
			cache.Range(func(_ string, value *strmatcher.MphValueMatcher) bool {
				compiled = append(compiled, weak.Make(value))
				return true
			})
			if len(compiled) != 1 {
				t.Fatalf("compiled entries=%d want 1", len(compiled))
			}
			for range 3 {
				runtime.GC()
				if !m.MatchAny("163.com") || m.MatchAny("does-not-exist.invalid") {
					t.Fatal("live DAT matcher changed after GC")
				}
			}
			runtime.ReadMemStats(&held)
			runtime.KeepAlive(m)
			m = nil
			awaitCollection(t, func() bool { return allCollected(compiled) })
			runtime.GC()
			runtime.ReadMemStats(&released)
			t.Logf("heap baseline=%d sampled_build_peak=%d held_post_gc=%d released_post_gc=%d objects baseline=%d held=%d released=%d compiled_collected=%d", baseline.HeapAlloc, peak, held.HeapAlloc, released.HeapAlloc, baseline.HeapObjects, held.HeapObjects, released.HeapObjects, len(compiled))
			runtime.KeepAlive(factory)
			runtime.KeepAlive(cache)
		})
	}
}

func TestU012ConcurrentRegistryReloadAndGC(t *testing.T) {
	collectionFixture(t)
	registry := newDomainRegistry()
	m, err := registry.BuildDomainMatcher([]*DomainRule{{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: "site.dat", Code: "TEST"}}}})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if !m.MatchAny("example.test") || m.MatchAny("other.test") {
					t.Error("concurrent reload lost live matcher")
					return
				}
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()
	for range 30 {
		if err := registry.Reload(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
	}
}

func BenchmarkU012ProductionLookup(b *testing.B) {
	b.Setenv("xray.location.asset", filepath.Join("..", "..", "resources"))
	for _, compact := range []bool{false, true} {
		b.Run(fmt.Sprintf("compact=%v", compact), func(b *testing.B) {
			cache := utils.NewWeakCacheMap[string, strmatcher.MphValueMatcher]()
			var factory DomainMatcherFactory = &MphDomainMatcherFactory{shared: cache}
			if compact {
				factory = &CompactMphDomainMatcherFactory{shared: cache}
			}
			m, err := factory.BuildMatcher([]*DomainRule{{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: DefaultGeoSiteDat, Code: "CN"}}}})
			if err != nil {
				b.Fatal(err)
			}
			queries := []string{"163.com", "www.baidu.com", "does-not-exist.invalid"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.MatchAny(queries[i%len(queries)])
			}
			runtime.KeepAlive(m)
		})
	}
}
