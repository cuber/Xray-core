package geodata

import (
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
	"github.com/xtls/xray-core/common/uuid"
	"google.golang.org/protobuf/proto"
)

func awaitCollection(t *testing.T, gone func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if gone() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retired objects remained reachable after bounded GC retries")
}

func allCollected[T any](refs []weak.Pointer[T]) bool {
	for _, ref := range refs {
		if ref.Value() != nil {
			return false
		}
	}
	return true
}

func collectionFixture(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for name, message := range map[string]proto.Message{
		"site.dat": &GeoSiteList{Entry: []*GeoSite{{Code: "TEST", Domain: []*Domain{{Type: Domain_Domain, Value: "example.test"}}}}},
		"ip.dat":   &GeoIPList{Entry: []*GeoIP{{Code: "TEST", Cidr: []*CIDR{{Ip: []byte{10, 0, 0, 0}, Prefix: 8}}}}},
	} {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("xray.location.asset", dir)
}

func TestU012DomainRegistryCollectibility(t *testing.T) {
	collectionFixture(t)
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%v", compact), func(t *testing.T) {
			cache := utils.NewWeakCacheMap[string, strmatcher.MphValueMatcher]()
			var factory DomainMatcherFactory = &MphDomainMatcherFactory{shared: cache}
			if compact {
				factory = &CompactMphDomainMatcherFactory{shared: cache}
			}
			registry := &DomainRegistry{factory: factory, matchers: utils.NewWeakCacheMap[uuid.UUID, DynamicDomainMatcher]()}
			rules := []*DomainRule{{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: "site.dat", Code: "TEST"}}}}
			active, err := registry.BuildDomainMatcher(rules)
			if err != nil {
				t.Fatal(err)
			}
			var retired []weak.Pointer[DynamicDomainMatcher]
			var states []weak.Pointer[domainMatcherState]
			var compiled []weak.Pointer[strmatcher.MphValueMatcher]
			var retiredCaches []weak.Pointer[utils.WeakCacheMap[string, strmatcher.MphValueMatcher]]
			cache.Range(func(_ string, m *strmatcher.MphValueMatcher) bool {
				compiled = append(compiled, weak.Make(m))
				return true
			})
			for round := range 300 {
				// Keep the original factory alive as a weak-value negative control;
				// subsequent reload factories and their caches must be collectible.
				if round > 0 {
					switch f := registry.factory.(type) {
					case *MphDomainMatcherFactory:
						retiredCaches = append(retiredCaches, weak.Make(f.shared))
					case *CompactMphDomainMatcherFactory:
						retiredCaches = append(retiredCaches, weak.Make(f.shared))
					}
				}
				m, err := registry.BuildDomainMatcher(rules)
				if err != nil {
					t.Fatal(err)
				}
				retired = append(retired, weak.Make(m.(*DynamicDomainMatcher)))
				states = append(states, weak.Make(active.(*DynamicDomainMatcher).state.Load()))
				runtime.GC()
				if err := registry.Reload(); err != nil {
					t.Fatal(err)
				}
				if !active.MatchAny("a.example.test") || active.MatchAny("notexample.test") {
					t.Fatal("live matcher changed across reload")
				}
			}
			awaitCollection(t, func() bool {
				return allCollected(retired) && allCollected(states) && allCollected(compiled) && allCollected(retiredCaches)
			})
			count := 0
			registry.matchers.Range(func(_ uuid.UUID, _ *DynamicDomainMatcher) bool { count++; return true })
			if count != 1 {
				t.Fatalf("registry live entries=%d want 1", count)
			}
			if !active.MatchAny("example.test") {
				t.Fatal("GC collected live matcher")
			}
			t.Logf("churn=300 retired_dynamic=%d retired_states=%d initial_compiled=%d collected; live_registry=%d", len(retired), len(states), len(compiled), count)
			t.Logf("retired_factory_caches_collected=%d", len(retiredCaches))
			runtime.KeepAlive(active)
			runtime.KeepAlive(registry)
			runtime.KeepAlive(factory)
		})
	}
}

func TestU012IPRegistryCollectibility(t *testing.T) {
	collectionFixture(t)
	registry := newIPRegistry()
	rules := []*IPRule{{Value: &IPRule_Geoip{Geoip: &GeoIPRule{File: "ip.dat", Code: "TEST"}}}}
	active, err := registry.BuildIPMatcher(rules)
	if err != nil {
		t.Fatal(err)
	}
	var retired []weak.Pointer[DynamicIPMatcher]
	var states []weak.Pointer[ipMatcherState]
	var sets []weak.Pointer[IPSet]
	var retiredCaches []weak.Pointer[utils.WeakCacheMap[string, IPSet]]
	for range 300 {
		retiredCaches = append(retiredCaches, weak.Make(registry.factory.shared))
		m, err := registry.BuildIPMatcher(rules)
		if err != nil {
			t.Fatal(err)
		}
		retired = append(retired, weak.Make(m.(*DynamicIPMatcher)))
		states = append(states, weak.Make(active.(*DynamicIPMatcher).state.Load()))
		registry.factory.shared.Range(func(_ string, set *IPSet) bool { sets = append(sets, weak.Make(set)); return true })
		runtime.GC()
		if err := registry.Reload(); err != nil {
			t.Fatal(err)
		}
		if !active.Match([]byte{10, 1, 2, 3}) || active.Match([]byte{11, 1, 2, 3}) {
			t.Fatal("live IP matcher changed")
		}
	}
	awaitCollection(t, func() bool {
		return allCollected(retired) && allCollected(states) && allCollected(sets) && allCollected(retiredCaches)
	})
	count := 0
	registry.matchers.Range(func(_ uuid.UUID, _ *DynamicIPMatcher) bool { count++; return true })
	if count != 1 {
		t.Fatalf("registry live entries=%d want 1", count)
	}
	t.Logf("churn=300 retired_dynamic=%d retired_states=%d retired_ipsets=%d collected; live_registry=%d", len(retired), len(states), len(sets), count)
	t.Logf("retired_factory_caches_collected=%d", len(retiredCaches))
	runtime.KeepAlive(active)
	runtime.KeepAlive(registry)
}

func TestU012WeakCacheReplacementAndConcurrentGC(t *testing.T) {
	cache := utils.NewWeakCacheMap[string, DynamicDomainMatcher]()
	rules := []*DomainRule{{Value: &DomainRule_Custom{Custom: &Domain{Type: Domain_Full, Value: "live.test"}}}}
	makeMatcher := func() *DynamicDomainMatcher {
		m, err := newDomainMatcherFactory().BuildMatcher(rules)
		if err != nil {
			t.Fatal(err)
		}
		return NewDynamicDomainMatcher(rules, m)
	}
	old := makeMatcher()
	oldRef := weak.Make(old)
	cache.Store("same", old)
	replacement := makeMatcher()
	cache.Store("same", replacement)
	old = nil
	awaitCollection(t, func() bool { return oldRef.Value() == nil })
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				cache.Store(fmt.Sprint(worker), replacement)
				if m, ok := cache.Load("same"); !ok || m != replacement || !m.MatchAny("live.test") {
					t.Error("old cleanup deleted replacement")
				}
				cache.Range(func(_ string, m *DynamicDomainMatcher) bool {
					if !m.MatchAny("live.test") {
						t.Error("bad live cache value")
					}
					return true
				})
			}
		}()
	}
	for range 10 {
		runtime.GC()
	}
	wg.Wait()
	if m, ok := cache.Load("same"); !ok || m != replacement {
		t.Fatal("replacement lost after GC")
	}
	runtime.KeepAlive(replacement)
}
