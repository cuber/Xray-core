package stats_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/features/stats"
)

func TestAtomicStatsFirstUseExactTotals(t *testing.T) {
	m, err := NewManager(context.Background(), &Config{DomainTraffic: &DomainTrafficConfig{Enabled: true, MaxDomains: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	const workers = 128
	for _, name := range []string{"first", "second"} {
		var previous stats.Counter
		for generation := 0; generation < 2; generation++ {
			counters := make([]stats.Counter, workers)
			online := make([]stats.OnlineMap, workers)
			channels := make([]stats.Channel, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					c, err := m.GetOrRegisterCounter(name)
					if err != nil {
						t.Error(err)
						return
					}
					counters[i] = c
					c.Add(int64(i + 1))
					om, err := m.GetOrRegisterOnlineMap(name)
					if err != nil {
						t.Error(err)
						return
					}
					online[i] = om
					om.AddIP(fmt.Sprintf("192.0.2.%d", i+1))
					ch, err := m.GetOrRegisterChannel(name)
					if err != nil {
						t.Error(err)
						return
					}
					channels[i] = ch
					if !ch.(*Channel).Running() {
						t.Error("published unstarted channel")
					}
					m.RecordDomainTraffic(fmt.Sprintf("%d.test", i), 1, 2, name)
				}(i)
			}
			close(start)
			wg.Wait()
			if t.Failed() {
				return
			}
			for i := range counters {
				if counters[i] != counters[0] || online[i] != online[0] || channels[i] != channels[0] {
					t.Fatal("registration split identity")
				}
			}
			c := counters[0]
			if c == previous || c.Value() != workers*(workers+1)/2 {
				t.Fatalf("counter generation or total: %v", c.Value())
			}
			if old := c.Set(0); old != workers*(workers+1)/2 {
				t.Fatalf("reset returned %d", old)
			}
			if c.Add(7) != 7 {
				t.Fatal("increment after reset")
			}
			if online[0].Count() != workers {
				t.Fatalf("online count = %d", online[0].Count())
			}
			if err := m.UnregisterCounter(name); err != nil {
				t.Fatal(err)
			}
			if err := m.UnregisterOnlineMap(name); err != nil {
				t.Fatal(err)
			}
			if err := m.UnregisterChannel(name); err != nil {
				t.Fatal(err)
			}
			if channels[0].(*Channel).Running() {
				t.Fatal("unregistered channel running")
			}
			previous = c
		}
	}
}

func TestAtomicStatsNoop(t *testing.T) {
	m := stats.NoopManager{}
	if c, err := m.GetOrRegisterCounter("test"); c != nil || err == nil {
		t.Fatal("noop counter")
	}
	if om, err := m.GetOrRegisterOnlineMap("test"); om != nil || err == nil {
		t.Fatal("noop online map")
	}
	if ch, err := m.GetOrRegisterChannel("test"); ch != nil || err == nil {
		t.Fatal("noop channel")
	}
}
