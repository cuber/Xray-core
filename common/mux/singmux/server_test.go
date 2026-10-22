package singmux

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sm "github.com/sagernet/sing-mux"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/buf"
	xctx "github.com/xtls/xray-core/common/ctx"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type dispatchRecord struct {
	dest  net.Destination
	tag   string
	email string
	id    xctx.ID
	sniff bool
}

// recordingDispatcher echoes every link and records what the streams carried.
type recordingDispatcher struct {
	mu      sync.Mutex
	records []dispatchRecord
	hold    chan struct{}
}

func (d *recordingDispatcher) Type() interface{} { return routing.DispatcherType() }
func (d *recordingDispatcher) Start() error      { return nil }
func (d *recordingDispatcher) Close() error      { return nil }

func (d *recordingDispatcher) Dispatch(ctx context.Context, dest net.Destination) (*transport.Link, error) {
	record := dispatchRecord{dest: dest, id: xctx.IDFromContext(ctx)}
	if inbound := session.InboundFromContext(ctx); inbound != nil {
		record.tag = inbound.Tag
		if inbound.User != nil {
			record.email = inbound.User.Email
		}
	}
	if content := session.ContentFromContext(ctx); content != nil {
		record.sniff = content.SniffingRequest.Enabled
	}
	d.mu.Lock()
	d.records = append(d.records, record)
	hold := d.hold
	d.mu.Unlock()
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	go func() {
		defer downWriter.Close()
		if hold != nil {
			<-hold
		}
		for {
			mb, err := upReader.ReadMultiBuffer()
			for _, b := range mb {
				if dest.Network == net.Network_UDP {
					from := dest
					b.UDP = &from
				}
			}
			if !mb.IsEmpty() && downWriter.WriteMultiBuffer(mb) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return &transport.Link{Reader: downReader, Writer: upWriter}, nil
}

func (d *recordingDispatcher) DispatchLink(context.Context, net.Destination, *transport.Link) error {
	return fmt.Errorf("unused")
}

func (d *recordingDispatcher) snapshot() []dispatchRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]dispatchRecord(nil), d.records...)
}

type testPolicy struct {
	handshake time.Duration
}

func (testPolicy) Type() interface{} { return policy.ManagerType() }
func (testPolicy) Start() error      { return nil }
func (testPolicy) Close() error      { return nil }
func (p testPolicy) ForLevel(uint32) policy.Session {
	s := policy.SessionDefault()
	if p.handshake > 0 {
		s.Timeouts.Handshake = p.handshake
	}
	return s
}
func (testPolicy) ForSystem() policy.System { return policy.System{} }

func newTestServer(t *testing.T, options Options) (*Server, *recordingDispatcher) {
	t.Helper()
	s := newServer(options)
	d := &recordingDispatcher{}
	s.dispatcher = d
	s.policy = testPolicy{handshake: 2 * time.Second}
	t.Cleanup(func() { s.Close() })
	return s, d
}

func carrierContext() context.Context {
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{
		Tag:    "ss-in",
		Source: net.TCPDestination(net.LocalHostIP, 40000),
		User:   &protocol.MemoryUser{Email: "alice@auto"},
	})
	return session.ContextWithContent(ctx, &session.Content{SniffingRequest: session.SniffingRequest{Enabled: true}})
}

// carrierDialer gives the client one in-memory carrier per dial, served by s.
type carrierDialer struct {
	s     *Server
	ctx   context.Context
	dials atomic.Int32
	serve chan error
}

func (d *carrierDialer) DialContext(_ context.Context, network string, dest M.Socksaddr) (net.Conn, error) {
	if dest != sm.Destination {
		return nil, fmt.Errorf("unexpected carrier destination %s", dest)
	}
	d.dials.Add(1)
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	go func() {
		err := d.s.Serve(d.ctx, &transport.Link{Reader: upReader, Writer: downWriter})
		if d.serve != nil {
			d.serve <- err
		}
	}()
	return cnc.NewConnection(cnc.ConnectionInputMulti(upWriter), cnc.ConnectionOutputMulti(downReader)), nil
}

func (d *carrierDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func newClient(t *testing.T, s *Server, protocol string, padding bool) (*sm.Client, *carrierDialer) {
	t.Helper()
	dialer := &carrierDialer{s: s, ctx: carrierContext()}
	client, err := sm.NewClient(sm.Options{Dialer: dialer, Protocol: protocol, MaxConnections: 1, MinStreams: 1 << 20, Padding: padding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, dialer
}

func roundTrip(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal("write:", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal("read:", err)
	}
	if string(got) != payload {
		t.Fatalf("echo %q, want %q", got, payload)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for ", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamsCarryTheCarrierIdentity(t *testing.T) {
	for _, protocol := range []string{"h2mux", "smux", "yamux"} {
		for _, padding := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/padding=%v", protocol, padding), func(t *testing.T) {
				s, d := newTestServer(t, Options{Enabled: true})
				client, dialer := newClient(t, s, protocol, padding)
				const streams = 20
				var wg sync.WaitGroup
				errs := make(chan error, streams)
				for i := 0; i < streams; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("example.test", 443))
						if err != nil {
							errs <- err
							return
						}
						defer conn.Close()
						payload := strings.Repeat(fmt.Sprintf("stream-%02d|", i), 64)
						conn.SetDeadline(time.Now().Add(10 * time.Second))
						if _, err := conn.Write([]byte(payload)); err != nil {
							errs <- err
							return
						}
						got := make([]byte, len(payload))
						if _, err := io.ReadFull(conn, got); err != nil {
							errs <- err
							return
						}
						if string(got) != payload {
							errs <- fmt.Errorf("stream %d echoed %d bytes", i, len(got))
						}
					}(i)
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					t.Fatal(err)
				}
				if n := dialer.dials.Load(); n != 1 {
					t.Fatalf("client dialed %d carriers, want 1", n)
				}
				records := d.snapshot()
				if len(records) != streams {
					t.Fatalf("dispatched %d streams, want %d", len(records), streams)
				}
				ids := map[xctx.ID]bool{}
				for _, r := range records {
					if r.dest != net.TCPDestination(net.DomainAddress("example.test"), 443) || r.tag != "ss-in" || r.email != "alice@auto" || !r.sniff {
						t.Fatalf("stream lost the carrier identity: %+v", r)
					}
					ids[r.id] = true
				}
				if len(ids) != streams {
					t.Fatalf("streams share session IDs: %d distinct of %d", len(ids), streams)
				}
				snapshot := s.Snapshot()
				if snapshot.TotalCarriers != 1 || snapshot.TotalStreams != streams || snapshot.ActiveCarriers[protocol] != 1 {
					t.Fatalf("unexpected counters: %+v", snapshot)
				}
				if wantPadded := uint64(0); padding {
					wantPadded = 1
					if snapshot.ActivePadded != wantPadded {
						t.Fatalf("padded carriers %d, want %d", snapshot.ActivePadded, wantPadded)
					}
				}
				waitFor(t, "streams to close", func() bool { return s.Snapshot().ActiveTCPStreams == 0 })
				client.Close()
				waitFor(t, "carrier to close", func() bool { return s.Snapshot().ActiveCarriers[protocol] == 0 })
			})
		}
	}
}

func TestPacketStreams(t *testing.T) {
	for _, protocol := range []string{"h2mux", "smux", "yamux"} {
		t.Run(protocol, func(t *testing.T) {
			s, d := newTestServer(t, Options{Enabled: true})
			client, _ := newClient(t, s, protocol, true)
			// PacketAddr streams carry the destination with every packet, as mihomo sends
			// them; like mihomo, open with a real destination (the client mis-sizes an empty one).
			packets, err := client.ListenPacket(context.Background(), M.ParseSocksaddrHostPort("1.1.1.1", 53))
			if err != nil {
				t.Fatal(err)
			}
			defer packets.Close()
			packets.SetDeadline(time.Now().Add(10 * time.Second))
			for i, target := range []M.Socksaddr{M.ParseSocksaddrHostPort("1.1.1.1", 53), M.ParseSocksaddrHostPort("8.8.8.8", 5353)} {
				payload := []byte(fmt.Sprintf("packet-%d", i))
				if _, err := packets.WriteTo(payload, target.UDPAddr()); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, 64)
				n, from, err := packets.ReadFrom(got)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got[:n], payload) || from.String() != target.String() {
					t.Fatalf("packet %q from %s, want %q from %s", got[:n], from, payload, target)
				}
			}
			// Fixed-destination streams, as sing-box sends them.
			fixed, err := client.DialContext(context.Background(), "udp", M.ParseSocksaddrHostPort("dns.test", 53))
			if err != nil {
				t.Fatal(err)
			}
			defer fixed.Close()
			roundTrip(t, fixed, "fixed-destination")
			found := false
			for _, r := range d.snapshot() {
				if r.dest == net.UDPDestination(net.DomainAddress("dns.test"), 53) && r.email == "alice@auto" {
					found = true
				}
			}
			if !found {
				t.Fatalf("fixed-destination packet stream was not dispatched as UDP: %+v", d.snapshot())
			}
			if snapshot := s.Snapshot(); snapshot.ActiveUDPStreams != 2 {
				t.Fatalf("active UDP streams %d, want 2", snapshot.ActiveUDPStreams)
			}
		})
	}
}

func readRefusal(t *testing.T, conn net.Conn) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	conn.Write([]byte("x"))
	_, err := conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("stream was not refused")
	}
	return err.Error()
}

func TestStreamLimit(t *testing.T) {
	s, d := newTestServer(t, Options{Enabled: true, MaxStreams: 2})
	d.hold = make(chan struct{})
	defer close(d.hold)
	client, _ := newClient(t, s, "h2mux", false)
	for i := 0; i < 2; i++ {
		conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("held.test", 80))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.Write([]byte("open"))
	}
	waitFor(t, "two admitted streams", func() bool { return s.Snapshot().ActiveTCPStreams == 2 })
	conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("third.test", 80))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if msg := readRefusal(t, conn); !strings.Contains(msg, "limit") {
		t.Fatalf("third stream error %q, want a limit refusal", msg)
	}
	if got := s.Snapshot().Rejected[rejectLimit]; got != 1 {
		t.Fatalf("limit refusals %d, want 1", got)
	}
}

func TestRevokedUserKeepsOpenStreams(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	s, _ := newTestServer(t, Options{Enabled: true, UserActive: func(_ context.Context, email string) bool {
		return email == "alice@auto" && active.Load()
	}})
	client, _ := newClient(t, s, "smux", false)
	open, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("open.test", 80))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	roundTrip(t, open, "before")
	active.Store(false)
	refused, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("new.test", 80))
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	if msg := readRefusal(t, refused); !strings.Contains(msg, "revoked") {
		t.Fatalf("new stream error %q, want a revoked refusal", msg)
	}
	roundTrip(t, open, "after")
	if got := s.Snapshot().Rejected[rejectRevoked]; got != 1 {
		t.Fatalf("revoked refusals %d, want 1", got)
	}
}

func TestNestedAndInvalidTargetsAreRefused(t *testing.T) {
	s, d := newTestServer(t, Options{Enabled: true})
	client, _ := newClient(t, s, "yamux", false)
	for _, target := range []M.Socksaddr{sm.Destination, M.ParseSocksaddrHostPort("v1.mux.cool", 9527)} {
		conn, err := client.DialContext(context.Background(), "tcp", target)
		if err != nil {
			t.Fatal(err)
		}
		if msg := readRefusal(t, conn); !strings.Contains(msg, "nested") {
			t.Fatalf("%s error %q, want a nested refusal", target, msg)
		}
		conn.Close()
	}
	if len(d.snapshot()) != 0 {
		t.Fatal("refused targets were dispatched")
	}
	if got := s.Snapshot().Rejected[rejectNested]; got != 2 {
		t.Fatalf("nested refusals %d, want 2", got)
	}
}

func serveRaw(t *testing.T, s *Server, data []byte) error {
	t.Helper()
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	defer downReader.Interrupt()
	if len(data) > 0 {
		b := buf.New()
		b.Write(data)
		upWriter.WriteMultiBuffer(buf.MultiBuffer{b})
	}
	result := make(chan error, 1)
	go func() { result <- s.Serve(carrierContext(), &transport.Link{Reader: upReader, Writer: downWriter}) }()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func TestPreambleIsValidatedAndBounded(t *testing.T) {
	s, _ := newTestServer(t, Options{Enabled: true})
	if err := serveRaw(t, s, []byte{5, 0}); err == nil {
		t.Fatal("unsupported version accepted")
	}
	if err := serveRaw(t, s, []byte{0, 7}); err == nil {
		t.Fatal("unknown protocol accepted")
	}
	s.policy = testPolicy{handshake: 200 * time.Millisecond}
	start := time.Now()
	if err := serveRaw(t, s, nil); err == nil {
		t.Fatal("silent carrier accepted")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("silent carrier held for %s", elapsed)
	}
	snapshot := s.Snapshot()
	if snapshot.Rejected[rejectPreamble] != 3 || snapshot.TotalCarriers != 0 {
		t.Fatalf("unexpected counters: %+v", snapshot)
	}
}

func TestDisabledServerServesNothing(t *testing.T) {
	s, _ := newTestServer(t, Options{})
	if err := serveRaw(t, s, []byte{0, sm.ProtocolH2Mux}); err == nil {
		t.Fatal("disabled server accepted a carrier")
	}
	snapshot := s.Snapshot()
	if snapshot.Enabled || snapshot.TotalCarriers != 0 || snapshot.MaxStreams != DefaultMaxStreams {
		t.Fatalf("unexpected counters: %+v", snapshot)
	}
}

func TestCloseEndsCarriersAndStreams(t *testing.T) {
	s, d := newTestServer(t, Options{Enabled: true})
	d.hold = make(chan struct{})
	defer close(d.hold)
	client, dialer := newClient(t, s, "h2mux", false)
	dialer.serve = make(chan error, 1)
	conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("held.test", 80))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("open"))
	waitFor(t, "an admitted stream", func() bool { return s.Snapshot().ActiveTCPStreams == 1 })
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not join the carrier")
	}
	<-dialer.serve
	snapshot := s.Snapshot()
	if snapshot.ActiveCarriers["h2mux"] != 0 || snapshot.ActiveTCPStreams != 0 {
		t.Fatalf("carrier state survived Close: %+v", snapshot)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("stream survived Close")
	}
}

func TestBrutalIsRefused(t *testing.T) {
	s, _ := newTestServer(t, Options{Enabled: true})
	dialer := &carrierDialer{s: s, ctx: carrierContext()}
	client, err := sm.NewClient(sm.Options{
		Dialer: dialer, Protocol: "h2mux", MaxConnections: 1,
		Brutal: sm.BrutalOptions{Enabled: true, SendBPS: 1 << 20, ReceiveBPS: 1 << 20},
	})
	if err != nil {
		t.Skip("brutal client unavailable:", err)
	}
	defer client.Close()
	conn, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("example.test", 80))
	if err == nil {
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		conn.Write([]byte("x"))
		_, err = conn.Read(make([]byte, 1))
		conn.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "brutal") {
		t.Fatalf("brutal session error %v, want the server refusal", err)
	}
}

func TestIsDestination(t *testing.T) {
	for _, c := range []struct {
		dest net.Destination
		want bool
	}{
		{net.TCPDestination(net.DomainAddress(Domain), Port), true},
		{net.TCPDestination(net.DomainAddress("SP.MUX.SING-BOX.ARPA"), Port), true},
		{net.TCPDestination(net.DomainAddress(Domain), 443), false},
		{net.TCPDestination(net.DomainAddress("v1.mux.cool"), Port), false},
		{net.TCPDestination(net.LocalHostIP, Port), false},
	} {
		if got := IsDestination(c.dest); got != c.want {
			t.Errorf("IsDestination(%s) = %v, want %v", c.dest, got, c.want)
		}
	}
}
