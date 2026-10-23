package singmux

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sm "github.com/sagernet/sing-mux"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

// endingDialer serves each carrier the way the SS-2022 inbound does through
// mux.Server.Dispatch: Serve runs on the connection's own context, and when the
// connection ends the inbound interrupts the link while its worker cancels that
// context.
type endingDialer struct {
	s    *Server
	mu   sync.Mutex
	ends []func()
}

func (d *endingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	ctx, cancel := context.WithCancel(carrierContext())
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	link := &transport.Link{Reader: upReader, Writer: downWriter}
	go d.s.Serve(ctx, link)
	d.mu.Lock()
	d.ends = append(d.ends, func() {
		go cancel()
		common.Interrupt(link.Reader)
		common.Interrupt(link.Writer)
	})
	d.mu.Unlock()
	return cnc.NewConnection(cnc.ConnectionInputMulti(upWriter), cnc.ConnectionOutputMulti(downReader)), nil
}

func (d *endingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (d *endingDialer) endAll() {
	d.mu.Lock()
	ends := d.ends
	d.ends = nil
	d.mu.Unlock()
	var wg sync.WaitGroup
	for _, end := range ends {
		wg.Add(1)
		go func() { defer wg.Done(); end() }()
	}
	wg.Wait()
}

// openCarriers dials n single-carrier h2mux clients and proves each with an echo.
func openCarriers(t *testing.T, dialer *endingDialer, n int) []*sm.Client {
	t.Helper()
	clients := make([]*sm.Client, n)
	for i := range clients {
		client, err := sm.NewClient(sm.Options{Dialer: dialer, Protocol: "h2mux", MaxConnections: 1, MinStreams: 1 << 20, Padding: i%2 == 0})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("echo.test", 80))
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, conn, "ping")
		clients[i] = client
	}
	return clients
}

// sing-mux v0.3.7 closed an h2mux server session from two goroutines, after
// ServeConn returned and in the service's cleanup, each with an unguarded
// close(done). A carrier whose context was cancelled while its link closed ran
// both at once and crashed the process with "close of closed channel"
// (Core spec013, amendment of 2026-10-23). End many carriers that way.
func TestEndingCarriersDoNotRaceTheH2MuxSession(t *testing.T) {
	const rounds, carriers = 25, 16
	for round := 0; round < rounds; round++ {
		s, _ := newTestServer(t, Options{Enabled: true})
		dialer := &endingDialer{s: s}
		clients := openCarriers(t, dialer, carriers)
		waitFor(t, "carriers", func() bool { return s.Snapshot().ActiveCarriers["h2mux"] == carriers })
		dialer.endAll()
		waitFor(t, "carriers to end", func() bool { return s.Snapshot().ActiveCarriers["h2mux"] == 0 })
		for _, client := range clients {
			client.Close()
		}
	}
}

func h2muxHandlers() int {
	stacks := make([]byte, 8<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	return strings.Count(string(stacks), "sing-mux.(*h2MuxServerSession).ServeHTTP")
}

// In v0.3.7 an h2mux stream that arrived as its carrier ended blocked forever
// handing itself to an Accept loop that had already returned. Open streams
// while carriers end and require every stream handler to return.
func TestEndingCarriersLeaveNoH2MuxHandlers(t *testing.T) {
	const rounds, carriers = 10, 16
	for round := 0; round < rounds; round++ {
		s, _ := newTestServer(t, Options{Enabled: true})
		dialer := &endingDialer{s: s}
		clients := openCarriers(t, dialer, carriers)
		var opening sync.WaitGroup
		stop := make(chan struct{})
		for _, client := range clients {
			opening.Add(1)
			go func() {
				defer opening.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					conn, err := client.DialContext(ctx, "tcp", M.ParseSocksaddrHostPort("echo.test", 80))
					cancel()
					if err != nil {
						return
					}
					conn.Write([]byte("x"))
					conn.Close()
				}
			}()
		}
		time.Sleep(20 * time.Millisecond)
		dialer.endAll()
		close(stop)
		opening.Wait()
		for _, client := range clients {
			client.Close()
		}
		waitFor(t, "carriers to end", func() bool { return s.Snapshot().ActiveCarriers["h2mux"] == 0 })
	}
	deadline := time.Now().Add(10 * time.Second)
	for h2muxHandlers() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := h2muxHandlers(); n > 0 {
		t.Fatalf("%d h2mux stream handlers never returned", n)
	}
}
