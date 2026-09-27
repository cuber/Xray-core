package anytls_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory/burst"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
)

// ANYTLS_SOAK_DURATION opts in. Strict acceptance cannot silently shorten the
// ten-minute gate; a short non-strict run is only fixture development evidence.
func TestAnyTLSOutboundSoak(t *testing.T) {
	text := os.Getenv("ANYTLS_SOAK_DURATION")
	if text == "" {
		if os.Getenv("ANYTLS_SOAK_STRICT") == "1" {
			t.Fatal("strict soak requires ANYTLS_SOAK_DURATION >= 10m")
		}
		t.Skip("set ANYTLS_SOAK_DURATION=10m and ANYTLS_SOAK_STRICT=1")
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		t.Fatalf("invalid soak duration %q: %v", text, err)
	}
	if os.Getenv("ANYTLS_SOAK_STRICT") == "1" && duration < 10*time.Minute {
		t.Fatal("strict soak duration must be >= 10m")
	}
	if deadline, ok := t.Deadline(); ok && time.Until(deadline) < duration+30*time.Second {
		t.Fatal("go test timeout must leave >=30s for teardown")
	}
	f, good := outboundFixture(t, "", func(config map[string]any) {
		// The original 600s wildcard run failed 3 UDP exchanges (1339/8192,
		// 0/8192, 0/8192), with all ownership cleanup passing. The independent
		// TestAnyTLSSoakUDPWildcardCollision reproduces Darwin selecting an
		// occupied IPv4 port for a dual-stack wildcard socket; replies reach
		// the other socket. Isolate this loopback fixture, not production code.
		for _, raw := range config["outbounds"].([]any) {
			out := raw.(map[string]any)
			if out["tag"] == "chain-exit" {
				out["sendThrough"] = "127.0.0.1"
			}
		}
		rules := config["routing"].(map[string]any)
		rules["rules"] = append([]any{map[string]any{
			"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "chain-exit",
		}}, rules["rules"].([]any)...)
	})
	echo := newSoakEcho(t)
	defer echo.close(t)
	var httpRequests, httpActive atomic.Int64
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpActive.Add(1)
		defer httpActive.Add(-1)
		httpRequests.Add(1)
		if r.URL.RequestURI() != "/generate_204?ob=soak" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	})}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); server.Serve(listener) }()
	defer func() {
		server.Close()
		soakWait(t, serveDone, "HTTP Serve")
		if httpActive.Load() != 0 {
			t.Error("HTTP handlers remain active")
		}
	}()
	destination, err := xnet.ParseDestination("tcp:" + echo.tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	udpDestination, err := xnet.ParseDestination("udp:" + echo.udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, dest xnet.Destination) (net.Conn, error) {
		return core.Dial(session.SetForcedOutboundTagToContext(ctx, "client"), f.instance, dest)
	}
	var packetID atomic.Uint64
	exchange := func(dest xnet.Destination, payload []byte) error {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := dial(ctx, dest)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		if dest.Network == xnet.Network_UDP && len(payload) >= 8 {
			binary.BigEndian.PutUint64(payload, packetID.Add(1))
		}
		if _, err := conn.Write(payload); err != nil {
			return fmt.Errorf("write after %s: %w (ctx=%v)", time.Since(started), err, ctx.Err())
		}
		got := make([]byte, len(payload))
		read := func() (int, error) { return io.ReadFull(conn, got) }
		if dest.Network == xnet.Network_UDP {
			// Packet APIs deliver one datagram per Read. Do not merge a short
			// packet with another packet or convert truncation into a timeout.
			got = make([]byte, 65535)
			read = func() (int, error) { return conn.Read(got) }
		}
		n, err := read()
		if err != nil {
			return fmt.Errorf("read %d/%d after %s: %w (ctx=%v echoUDP=%d/%d)", n, len(payload), time.Since(started), err, ctx.Err(), echo.udpReceived.Load(), echo.udpSent.Load())
		}
		if !bytes.Equal(got[:n], payload) {
			return fmt.Errorf("payload mismatch network=%s got=%d want=%d", dest.Network, n, len(payload))
		}
		return nil
	}
	probeCtx, probeCancel := context.WithCancel(context.WithValue(context.Background(), core.XrayKey(1), f.instance))
	defer probeCancel()
	probe := burst.NewHealthPing(probeCtx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
		Destination: "http://" + listener.Addr().String() + "/generate_204?ob=soak", HttpMethod: "HEAD", KeepAlive: true,
		Timeout: int64(time.Second), SamplingCount: 3, Interval: int64(time.Second),
	})
	check := func() error {
		if err := probe.Check([]string{"client"}); err != nil {
			return err
		}
		if result := probe.Results["client"]; result == nil || !result.Get().Alive {
			return fmt.Errorf("healthy OB became dead")
		}
		return nil
	}
	// Warm up TLS, pooled sessions, UDP and observer allocations before sampling.
	for range 20 {
		if err := exchange(destination, []byte("warmup")); err != nil {
			t.Fatal(err)
		}
		if err := exchange(udpDestination, []byte("warmup-udp")); err != nil {
			t.Fatal(err)
		}
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	soakResources(t, "warmed")
	var gate sync.RWMutex
	var workers sync.WaitGroup
	var activeWorkers, tcpOK, udpOK, obOK, cycles atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	errors := make(chan error, 64)
	var failureCount atomic.Int64
	launch := func(interval time.Duration, fatal bool, work func() error) {
		workers.Add(1)
		activeWorkers.Add(1)
		go func() {
			defer workers.Done()
			defer activeWorkers.Add(-1)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if err := work(); err != nil {
					failureCount.Add(1)
					select {
					case errors <- err:
					default:
					}
					t.Logf("workload failure (not retried): %v", err)
					if fatal {
						cancel()
						return
					}
				}
			}
		}()
	}
	for id := 0; id < 4; id++ {
		// This is a sustained lifecycle soak, not admission-overload testing.
		// FIN handlers may drain for a second; keep offered stream churn below
		// the server's 128-active-stream budget even while UDP/OB also run.
		launch(100*time.Millisecond, false, func() error {
			gate.RLock()
			defer gate.RUnlock()
			payload := bytes.Repeat([]byte(fmt.Sprintf("tcp-worker-%d;", id)), 2048)
			if err := exchange(destination, payload); err != nil {
				return fmt.Errorf("TCP: %w", err)
			}
			tcpOK.Add(1)
			return nil
		})
	}
	for id := 0; id < 2; id++ {
		launch(150*time.Millisecond, false, func() error {
			gate.RLock()
			defer gate.RUnlock()
			payload := bytes.Repeat([]byte{byte(id + 1)}, 8192)
			if err := exchange(udpDestination, payload); err != nil {
				return fmt.Errorf("UDP: %w", err)
			}
			udpOK.Add(1)
			return nil
		})
	}
	launch(250*time.Millisecond, false, func() error {
		gate.RLock()
		defer gate.RUnlock()
		if err := check(); err != nil {
			return err
		}
		obOK.Add(1)
		return nil
	})
	manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	// Coordinate mutation, not timing guesses: old transfer is known established
	// before removal. Other workers resume against the newly published handler.
	launch(5*time.Second, true, func() error {
		gate.Lock()
		defer gate.Unlock()
		controlCtx, controlCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer controlCancel()
		held, err := dial(controlCtx, destination)
		if err != nil {
			return err
		}
		defer held.Close()
		stop := context.AfterFunc(controlCtx, func() { held.Close() })
		defer stop()
		if _, err := held.Write([]byte("held")); err != nil {
			return err
		}
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(held, buffer); err != nil || string(buffer) != "held" {
			return fmt.Errorf("held initial: %q %v", buffer, err)
		}
		old := manager.GetHandler("client")
		if old == nil {
			return fmt.Errorf("client handler missing before retirement")
		}
		retirement := old.(outbound.RetiringHandler).Retirement()
		if _, err := f.api.RemoveOutbound(controlCtx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
			return err
		}
		if _, err := f.api.AddOutbound(controlCtx, &handler.AddOutboundRequest{Outbound: good}); err != nil {
			return err
		}
		if _, err := held.Write([]byte("live")); err != nil {
			return err
		}
		if _, err := io.ReadFull(held, buffer); err != nil || string(buffer) != "live" {
			return fmt.Errorf("retired transfer: %q %v", buffer, err)
		}
		held.Close()
		select {
		case <-retirement.Retire():
		case <-controlCtx.Done():
			return fmt.Errorf("retired pool did not drain")
		}
		if err := exchange(destination, []byte{255, 1, 2, 3}); err == nil {
			return fmt.Errorf("RST backend unexpectedly echoed")
		}
		if err := exchange(destination, []byte("after-reset")); err != nil {
			return fmt.Errorf("reset recovery: %w", err)
		}
		cycles.Add(1)
		return nil
	})
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	// Register cleanup before waiting so assertion failures cannot orphan work.
	defer func() { cancel(); soakWait(t, joined, "workload workers") }()
	started := time.Now()
	report := time.NewTicker(time.Minute)
	defer report.Stop()
	running := true
	for running {
		select {
		case <-ctx.Done():
			running = false
		case <-report.C:
			t.Logf("elapsed=%s tcp=%d udp=%d ob=%d retire/reset=%d", time.Since(started).Round(time.Second), tcpOK.Load(), udpOK.Load(), obOK.Load(), cycles.Load())
			soakResources(t, "interval")
		}
	}
	soakWait(t, joined, "workload workers")
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if failureCount.Load() != 0 {
		t.Errorf("soak recorded %d failed transactions; no retries or failure allowance", failureCount.Load())
	}
	if elapsed := time.Since(started); elapsed < duration-time.Second {
		t.Errorf("workload stopped early: %s < %s", elapsed, duration)
	}
	if activeWorkers.Load() != 0 {
		t.Error("owned workload workers remain")
	}
	if tcpOK.Load() == 0 || udpOK.Load() == 0 || obOK.Load() == 0 || cycles.Load() == 0 || echo.resets.Load() != cycles.Load() {
		t.Error("mixed workload or reset/retirement coverage missing")
	}
	probeCancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		if err := f.instance.Close(); err != nil {
			t.Error(err)
		}
	}()
	soakWait(t, closed, "Core instance and pool shutdown")
	echo.close(t)
	server.Close()
	soakWait(t, serveDone, "HTTP Serve")
	if echo.active.Load() != 0 || echo.readers.Load() != 0 || httpActive.Load() != 0 {
		t.Error("owned server workers remain after teardown")
	}
	soakResources(t, "after teardown (supplement, not leak proof)")
	t.Logf("COMPLETE elapsed=%s TCP=%d UDP=%d OB=%d HTTP=%d retirements=%d RST=%d owned_workers=%d echo_connections=%d echo_readers=%d HTTP_active=%d",
		time.Since(started), tcpOK.Load(), udpOK.Load(), obOK.Load(), httpRequests.Load(), cycles.Load(), echo.resets.Load(), activeWorkers.Load(), echo.active.Load(), echo.readers.Load(), httpActive.Load())
}

func soakWait(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		t.Fatalf("bounded teardown failed: %s", label)
	}
}

func soakResources(t *testing.T, label string) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	// Reuse the platform-separated helper. Unsupported platforms return -1,
	// without skipping the workload or its explicit ownership assertions.
	fd := openFDCount(t)
	t.Logf("resources %s: goroutines=%d fd=%d retained_heap=%d heap_objects=%d", label, runtime.NumGoroutine(), fd, mem.HeapAlloc, mem.HeapObjects)
}

type soakEcho struct {
	tcp                     net.Listener
	udp                     net.PacketConn
	mu                      sync.Mutex
	connections             map[net.Conn]struct{}
	workers                 sync.WaitGroup
	accepted                chan struct{}
	once                    sync.Once
	active, readers, resets atomic.Int64
	udpReceived, udpSent    atomic.Int64
}

func newSoakEcho(t *testing.T) *soakEcho {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	e := &soakEcho{tcp: tcp, udp: udp, connections: make(map[net.Conn]struct{}), accepted: make(chan struct{})}
	e.workers.Add(1)
	e.readers.Add(1)
	go func() {
		defer e.workers.Done()
		defer e.readers.Add(-1)
		buffer := make([]byte, 65535)
		for {
			n, peer, err := udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			e.udpReceived.Add(1)
			if _, err := udp.WriteTo(buffer[:n], peer); err != nil {
				return
			}
			e.udpSent.Add(1)
		}
	}()
	go func() {
		defer close(e.accepted)
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			e.mu.Lock()
			e.connections[conn] = struct{}{}
			e.mu.Unlock()
			e.active.Add(1)
			e.workers.Add(1)
			go func() {
				defer e.workers.Done()
				defer e.active.Add(-1)
				defer func() { conn.Close(); e.mu.Lock(); delete(e.connections, conn); e.mu.Unlock() }()
				var first [1]byte
				if _, err := io.ReadFull(conn, first[:]); err != nil {
					return
				}
				if first[0] == 255 {
					e.resets.Add(1)
					conn.(*net.TCPConn).SetLinger(0)
					return
				}
				if _, err := conn.Write(first[:]); err != nil {
					return
				}
				io.Copy(conn, struct{ io.Reader }{conn})
			}()
		}
	}()
	return e
}

func (e *soakEcho) close(t *testing.T) {
	e.once.Do(func() {
		e.tcp.Close()
		e.udp.Close()
		soakWait(t, e.accepted, "echo accept loop")
		e.mu.Lock()
		for conn := range e.connections {
			conn.Close()
		}
		e.mu.Unlock()
		done := make(chan struct{})
		go func() { e.workers.Wait(); close(done) }()
		soakWait(t, done, "echo workers")
		e.mu.Lock()
		remaining := len(e.connections)
		e.mu.Unlock()
		if remaining != 0 || e.active.Load() != 0 || e.readers.Load() != 0 {
			t.Errorf("echo leaked owned connections/workers: %d/%d/%d", remaining, e.active.Load(), e.readers.Load())
		}
	})
}
