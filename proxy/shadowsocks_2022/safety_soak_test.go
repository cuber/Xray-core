package shadowsocks_2022

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	fstats "github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type soakConn struct {
	net.Conn
	once  sync.Once
	owned *atomic.Int64
}

func (c *soakConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owned.Add(-1) })
	return err
}

type soakSample struct {
	fd, goroutines int
	heap, objects  uint64
}

func soakCoreWorkers() int {
	stack := make([]byte, 64<<10)
	for {
		n := runtime.Stack(stack, true)
		if n < len(stack) {
			stack = stack[:n]
			break
		}
		stack = make([]byte, len(stack)*2)
	}
	count := 0
	for _, g := range bytes.Split(stack, []byte("\n\n")) {
		if bytes.Contains(g, []byte("shadowsocks_2022.TransportTCP")) || bytes.Contains(g, []byte("shadowsocks_2022.(*MultiUserInbound).processTCP")) {
			count++
		}
	}
	return count
}

func sampleSoak(t *testing.T) soakSample {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fd := fdSample()
	if fd < 0 {
		t.Fatal("process FD inventory unavailable; soak acceptance requires a supported FD oracle")
	}
	return soakSample{fd, runtime.NumGoroutine(), m.HeapAlloc, m.HeapObjects}
}

// This is intentionally opt-in, but never skips FD measurement on Darwin/Linux.
// XRAY_SS2022_SOAK=1 defaults to one hour; a short duration validates the harness
// only. Run with -timeout greater than the requested duration plus drain time.
func TestSS2022ResourceSoak(t *testing.T) {
	if os.Getenv("XRAY_SS2022_SOAK") != "1" {
		t.Skip("opt-in: XRAY_SS2022_SOAK=1 [XRAY_SS2022_SOAK_DURATION=1h]")
	}
	duration := time.Hour
	if raw := os.Getenv("XRAY_SS2022_SOAK_DURATION"); raw != "" {
		var err error
		duration, err = time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			t.Fatalf("invalid soak duration %q", raw)
		}
	}
	i := safetyMulti(t)
	defer i.Close()
	keys := [][]byte{bytes.Repeat([]byte{2}, 16), bytes.Repeat([]byte{3}, 16)}
	names := []string{"alice", "bob"}
	manager, err := stats.NewManager(context.Background(), &stats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var up, down [2]fstats.Counter
	var expected [2]int64
	for j, name := range names {
		up[j], err = manager.RegisterCounter("user>>>" + name + ">>>traffic>>>uplink")
		if err != nil {
			t.Fatal(err)
		}
		down[j], err = manager.RegisterCounter("user>>>" + name + ">>>traffic>>>downlink")
		if err != nil {
			t.Fatal(err)
		}
		if err := i.AddUser(context.Background(), &protocol.MemoryUser{Email: name, Level: uint32(j + 1), Account: &MemoryAccount{Key: keys[j]}}); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var sockets, workers atomic.Int64
	var echoes sync.WaitGroup
	d := safetyDispatcher{call: func(ctx context.Context, dst xnet.Destination) (*transport.Link, error) {
		name := session.InboundFromContext(ctx).User.Email
		j := -1
		for k, n := range names {
			if n == name {
				j = k
			}
		}
		if j < 0 {
			return nil, fmt.Errorf("unauthorized dispatch identity %q", name)
		}
		if dst.Port != xnet.Port(1234+j) {
			return nil, fmt.Errorf("wrong route for user %s: %v", name, dst)
		}
		ur, uw := pipe.New(pipe.WithSizeLimit(1 << 20))
		dr, dw := pipe.New(pipe.WithSizeLimit(1 << 20))
		workers.Add(1)
		echoes.Add(1)
		go func() {
			// Publish zero ownership before releasing a WaitGroup waiter.
			defer echoes.Done()
			defer workers.Add(-1)
			defer ur.Interrupt()
			defer dw.Close()
			_ = buf.Copy(ur, &dispatcher.SizeStatWriter{Counter: down[j], Writer: dw})
		}()
		return &transport.Link{Reader: dr, Writer: &dispatcher.SizeStatWriter{Counter: up[j], Writer: uw}}, nil
	}}
	start := time.Now()
	var baseline soakSample
	var warm []soakSample
	var warmMin, warmMax uint64
	for wave := 0; time.Since(start) < duration || wave < 5; wave++ {
		var handlers, clients sync.WaitGroup
		errs := make(chan error, 20)
		var conns []*soakConn
		for user := range names {
			for _, kind := range []string{"valid", "silent", "partial", "wrong-key", "close"} {
				c, err := net.Dial("tcp", ln.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				s, err := ln.Accept()
				if err != nil {
					c.Close()
					t.Fatal(err)
				}
				sockets.Add(2)
				client := &soakConn{Conn: c, owned: &sockets}
				server := &soakConn{Conn: s, owned: &sockets}
				conns = append(conns, client, server)
				ctx := session.ContextWithInbound(context.Background(), &session.Inbound{})
				workers.Add(1)
				handlers.Add(1)
				go func() {
					defer handlers.Done()
					defer workers.Add(-1)
					_ = i.processTCP(ctx, server, d)
				}()
				if kind == "valid" {
					expected[user] += 64 + 65536
				}
				workers.Add(1)
				clients.Add(1)
				go func() {
					defer clients.Done()
					defer workers.Add(-1)
					defer client.Close()
					_ = client.SetDeadline(time.Now().Add(4 * time.Second))
					salt := make([]byte, 16)
					if _, err := rand.Read(salt); err != nil {
						errs <- err
						return
					}
					switch kind {
					case "close":
						return
					case "partial":
						_, _ = client.Write(salt)
					case "wrong-key":
						_, _ = WriteTCPRequest(client, i.method, [][]byte{i.masterPSK, bytes.Repeat([]byte{9}, 16)}, xnet.TCPDestination(xnet.LocalHostIP, 1234), salt, []byte("invalid"))
					case "valid":
						early := bytes.Repeat([]byte{byte(user + 1)}, 64)
						late := bytes.Repeat([]byte{byte(user + 11)}, 65536)
						w, err := WriteTCPRequest(client, i.method, [][]byte{i.masterPSK, keys[user]}, xnet.TCPDestination(xnet.LocalHostIP, xnet.Port(1234+user)), salt, early)
						if err != nil {
							errs <- err
							return
						}
						r, err := ReadTCPResponse(client, i.method, keys[user], salt)
						if err != nil {
							errs <- err
							return
						}
						readExact := func(want []byte) error {
							var got []byte
							for len(got) < len(want) {
								mb, e := r.ReadMultiBuffer()
								for _, b := range mb {
									got = append(got, b.Bytes()...)
								}
								buf.ReleaseMulti(mb)
								if e != nil {
									return e
								}
							}
							if !bytes.Equal(got, want) {
								return fmt.Errorf("user %s exact-byte mismatch: %d/%d", names[user], len(got), len(want))
							}
							return nil
						}
						if err = readExact(early); err != nil {
							errs <- err
							return
						}
						if err = w.WriteMultiBuffer(buf.MergeBytes(nil, late)); err != nil {
							errs <- err
							return
						}
						if err = readExact(late); err != nil {
							errs <- err
						}
						return
					}
					var one [1]byte
					_, err := client.Read(one[:])
					if err == nil {
						errs <- fmt.Errorf("%s unexpectedly readable", kind)
					} else if e, ok := err.(net.Error); ok && e.Timeout() {
						errs <- fmt.Errorf("%s socket not server-closed within budget", kind)
					}
				}()
			}
		}
		drained := make(chan struct{})
		go func() { clients.Wait(); handlers.Wait(); echoes.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(5 * time.Second):
			for _, c := range conns {
				c.Close()
			}
			select {
			case <-drained:
			case <-time.After(time.Second):
				t.Fatal("owned workers failed forced drain")
			}
			t.Fatal("wave exceeded five-second drain budget")
		}
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if t.Failed() {
			return
		}
		// task.Run can return on the first copy error before its sibling exits.
		// Verify those native copy workers drain too, not only fixture wrappers.
		deadline := time.Now().Add(time.Second)
		for soakCoreWorkers() != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("native TCP workers retained: %d", soakCoreWorkers())
			}
			time.Sleep(time.Millisecond)
		}
		if sockets.Load() != 0 || workers.Load() != 0 {
			t.Fatalf("owned resources retained: sockets=%d workers=%d", sockets.Load(), workers.Load())
		}
		for j := range names {
			if up[j].Value() != expected[j] || down[j].Value() != expected[j] {
				t.Fatalf("user=%s expected=%d uplink=%d downlink=%d", names[j], expected[j], up[j].Value(), down[j].Value())
			}
		}
		var pre runtime.MemStats
		runtime.ReadMemStats(&pre)
		sample := sampleSoak(t)
		// Stream samples instead of retaining an hour of testing.Log output in heap.
		fmt.Printf("SS2022 soak elapsed=%s wave=%d owned_connections=%d owned_goroutines=%d native_copy_goroutines=0 process_fd=%d process_goroutines=%d HeapAlloc=%d postGC_HeapAlloc=%d postGC_objects=%d alice_up/down=%d/%d bob_up/down=%d/%d\n", time.Since(start).Round(time.Millisecond), wave, sockets.Load(), workers.Load(), sample.fd, sample.goroutines, pre.HeapAlloc, sample.heap, sample.objects, up[0].Value(), down[0].Value(), up[1].Value(), down[1].Value())
		if wave < 5 {
			warm = append(warm, sample)
			if wave == 0 || sample.heap < warmMin {
				warmMin = sample.heap
			}
			if sample.heap > warmMax {
				warmMax = sample.heap
			}
			baseline = sample
			for _, s := range warm {
				if s.fd > baseline.fd {
					baseline.fd = s.fd
				}
				if s.goroutines > baseline.goroutines {
					baseline.goroutines = s.goroutines
				}
			}
			continue
		}
		// Process-wide diagnostics allow warmed runtime noise; owned counts remain exact.
		// Heap tolerance is derived from warm-up variance, with a 1 MiB runtime floor.
		allowance := 4 * (warmMax - warmMin)
		if allowance < 1<<20 {
			allowance = 1 << 20
		}
		if sample.fd > baseline.fd+2 || sample.goroutines > baseline.goroutines+2 || sample.heap > warmMax+allowance {
			t.Fatalf("resource plateau exceeded: warm=%+v heap_range=%d..%d allowance=%d after=%+v", baseline, warmMin, warmMax, allowance, sample)
		}
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("completed duration=%s requested=%s; owned connections/workers=0; final=%+v", time.Since(start), duration, sampleSoak(t))
}
