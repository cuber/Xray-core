package anytls

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
)

type resourceClientConn struct {
	net.Conn
	owned *atomic.Int64
	once  sync.Once
}

func (c *resourceClientConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owned.Add(-1) })
	return err
}

func TestClientCompletedResourceRounds(t *testing.T) {
	measure := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	var baseline uint64
	for round := 0; round < 24; round++ {
		if !t.Run(fmt.Sprint(round), func(t *testing.T) {
			client, original, _ := clientFixture(t)
			var owned atomic.Int64
			dialer := clientTestDialer{dial: func(ctx context.Context) (net.Conn, error) {
				conn, err := original.dial(ctx)
				if err != nil {
					return nil, err
				}
				owned.Add(1)
				return &resourceClientConn{Conn: conn, owned: &owned}, nil
			}}
			var workers sync.WaitGroup
			for id := range 8 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for range 8 {
						conn, stop := clientStream(t, client, dialer)
						err := clientExchange(conn, bytes.Repeat([]byte{byte(id + 1)}, 32768))
						stop()
						if err != nil {
							t.Error(err)
							return
						}
					}
				}()
			}
			workers.Wait()
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			pending, active := len(client.pending), len(client.active)
			client.mu.Unlock()
			if owned.Load() != 0 || pending != 0 || active != 0 {
				t.Fatalf("owned sockets=%d pending=%d active=%d", owned.Load(), pending, active)
			}
			select {
			case <-client.done:
			default:
				t.Fatal("client shutdown did not finish")
			}
			// clientFixture cleanup joins the peer workers and listener before
			// the parent samples memory for this completed round.
		}) {
			return
		}
		heap := measure()
		if round == 3 {
			baseline = heap
		}
		if round >= 3 {
			t.Logf("completed round=%d retained_heap=%d baseline=%d", round, heap, baseline)
			// Supplement exact ownership assertions with a bounded heap check;
			// do not mistake race/runtime cache variation for a protocol leak.
			if heap > baseline+8*1024*1024 {
				// Keep a failing profile outside the worktree and past test cleanup.
				path := ""
				f, err := os.CreateTemp("", "anytls-heap-*.pprof")
				if err == nil {
					path = f.Name()
					err = pprof.WriteHeapProfile(f)
					f.Close()
				}
				t.Fatalf("retained heap growth: baseline=%d current=%d profile=%s err=%v", baseline, heap, path, err)
			}
		}
	}
}
