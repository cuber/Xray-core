package shadowsocks_2022

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/antireplay"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/utils"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type safetyPolicy struct{ handshake time.Duration }

type safetyLevelPolicy struct {
	safetyPolicy
	levels chan uint32
}

func (p safetyLevelPolicy) ForLevel(level uint32) policy.Session {
	p.levels <- level
	s := p.safetyPolicy.ForLevel(level)
	if level == 0 {
		s.Timeouts.ConnectionIdle = 10 * time.Millisecond
	}
	return s
}

func (p safetyPolicy) Type() interface{}        { return policy.ManagerType() }
func (p safetyPolicy) Start() error             { return nil }
func (p safetyPolicy) Close() error             { return nil }
func (p safetyPolicy) ForSystem() policy.System { return policy.System{} }
func (p safetyPolicy) ForLevel(level uint32) policy.Session {
	s := policy.SessionDefault()
	s.Timeouts.Handshake = p.handshake
	s.Timeouts.ConnectionIdle = 10 * time.Second
	return s
}

type safetyConn struct {
	net.Conn
	mu                 sync.Mutex
	deadlines          []time.Time
	failSet, failClear bool
	closed             atomic.Int64
	eofOnData          bool
	readEOF            atomic.Bool
}

func (c *safetyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.eofOnData && c.readEOF.CompareAndSwap(false, true) {
		return n, io.EOF
	}
	return n, err
}

func (c *safetyConn) SetReadDeadline(d time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadlines = append(c.deadlines, d)
	if (d.IsZero() && c.failClear) || (!d.IsZero() && c.failSet) {
		return io.ErrUnexpectedEOF
	}
	return c.Conn.SetReadDeadline(d)
}
func (c *safetyConn) Close() error { c.closed.Add(1); return c.Conn.Close() }

func TestSafetyLongTCPAndDeadlineErrors(t *testing.T) {
	for _, variant := range []string{"single", "multi", "relay"} {
		for _, failure := range []string{"none", "set", "clear", "data-eof"} {
			t.Run(variant+"/"+failure, func(t *testing.T) {
				multi := safetyMulti(t)
				levels := make(chan uint32, 4)
				multi.policyManager = safetyLevelPolicy{safetyPolicy{time.Second}, levels}
				key := bytes.Repeat([]byte{2}, 16)
				u := &protocol.MemoryUser{Email: "a", Level: 7, Account: &MemoryAccount{Key: key}}
				if err := multi.AddUser(context.Background(), u); err != nil {
					t.Fatal(err)
				}
				single := &Inbound{method: multi.method, psk: key, user: u, saltFilter: antireplay.NewMapFilter[[32]byte](60), policyManager: multi.policyManager}
				dst := xnet.TCPDestination(xnet.LocalHostIP, 1234)
				relay := &RelayInbound{method: multi.method, relayPSK: multi.masterPSK, policyManager: multi.policyManager, destinations: map[[16]byte]*relayDest{DeriveUserPSKHash(key): {destination: dst, email: "a", level: 7}}}
				process := single.processTCP
				keys := [][]byte{key}
				if variant == "multi" {
					process = multi.processTCP
					keys = [][]byte{multi.masterPSK, key}
				}
				if variant == "relay" {
					process = relay.processTCP
					keys = [][]byte{multi.masterPSK, key}
				}
				client, raw := net.Pipe()
				defer client.Close()
				server := &safetyConn{Conn: raw, failSet: failure == "set", failClear: failure == "clear", eofOnData: failure == "data-eof"}
				defer server.Close()
				_ = client.SetDeadline(time.Now().Add(6 * time.Second))
				ctx, cancel := context.WithCancel(session.ContextWithInbound(context.Background(), &session.Inbound{User: u}))
				defer cancel()
				var dispatches atomic.Int64
				r, w := pipe.New(pipe.WithSizeLimit(1 << 20))
				defer r.Interrupt()
				defer w.Interrupt()
				d := safetyDispatcher{call: func(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
					dispatches.Add(1)
					if session.InboundFromContext(ctx).User.Email != "a" {
						t.Error("incorrect identity")
					}
					return &transport.Link{Reader: r, Writer: w}, nil
				}}
				done := make(chan error, 1)
				go func() { done <- process(ctx, server, d) }()
				salt := bytes.Repeat([]byte{3}, 16)
				var writer buf.Writer
				var writeErr error
				var relayWire []byte
				if variant == "relay" {
					var wire bytes.Buffer
					_, writeErr = WriteTCPRequest(&wire, multi.method, keys, dst, salt, []byte("early"))
					relayWire = append(append([]byte(nil), wire.Bytes()[:16]...), wire.Bytes()[32:]...)
					if writeErr == nil {
						_, writeErr = client.Write(wire.Bytes())
					}
				} else {
					writer, writeErr = WriteTCPRequest(client, multi.method, keys, dst, salt, []byte("early"))
				}
				if failure == "set" || failure == "clear" {
					select {
					case err := <-done:
						if err == nil {
							t.Error("deadline failure ignored")
						}
					case <-time.After(2 * time.Second):
						t.Fatal("handler hung")
					}
					if dispatches.Load() != 0 || server.closed.Load() == 0 {
						t.Fatal("failed handshake dispatched or retained socket")
					}
					return
				}
				if writeErr != nil {
					t.Fatal(writeErr)
				}
				if variant == "relay" {
					// Relay forwards the inner handshake unchanged; consume it before raw echo.
					got := make([]byte, len(relayWire))
					if _, err := io.ReadFull(client, got); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, relayWire) {
						t.Fatal("relay handshake changed")
					}
				} else {
					reader, err := ReadTCPResponse(client, multi.method, key, salt)
					if err != nil {
						t.Fatal(err)
					}
					mb, err := reader.ReadMultiBuffer()
					if err != nil {
						t.Fatal(err)
					}
					var early []byte
					for _, b := range mb {
						early = append(early, b.Bytes()...)
					}
					buf.ReleaseMulti(mb)
					if string(early) != "early" {
						t.Fatalf("early=%q", early)
					}
					// Keep the same decoder for the subsequent stream frames.
					time.Sleep(3 * time.Second)
					if err := writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("late"))); err != nil {
						t.Fatal(err)
					}
					mb, err = reader.ReadMultiBuffer()
					if err != nil {
						t.Fatal(err)
					}
					var late []byte
					for _, b := range mb {
						late = append(late, b.Bytes()...)
					}
					buf.ReleaseMulti(mb)
					if string(late) != "late" {
						t.Fatalf("late=%q", late)
					}
				}
				if variant == "relay" {
					time.Sleep(3 * time.Second)
					if _, err := client.Write([]byte("late")); err != nil {
						t.Fatal(err)
					}
					var got [4]byte
					if _, err := io.ReadFull(client, got[:]); err != nil || string(got[:]) != "late" {
						t.Fatalf("relay late=%q err=%v", got, err)
					}
				}
				server.mu.Lock()
				ds := append([]time.Time(nil), server.deadlines...)
				server.mu.Unlock()
				if len(ds) != 2 || ds[0].IsZero() || !ds[1].IsZero() {
					t.Fatalf("deadlines=%v", ds)
				}
				if first, second := <-levels, <-levels; first != 0 || second != 7 {
					t.Fatalf("policy levels=%d,%d", first, second)
				}
				cancel()
				client.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("cancel did not join")
				}
			})
		}
	}
}

func TestSafetyUDPRevocationReplayAndExpiry(t *testing.T) {
	i := safetyMulti(t)
	rpc := safetyRPC(t, i)
	ctx := context.Background()
	users := []*protocol.MemoryUser{{Email: "a", Account: &MemoryAccount{Key: bytes.Repeat([]byte{2}, 16)}}, {Email: "b", Account: &MemoryAccount{Key: bytes.Repeat([]byte{3}, 16)}}}
	clients := make([]*ClientUDPSession, 2)
	for j, u := range users {
		if e := safetyAlter(t, rpc, serial.ToTypedMessage(&command.AddUserOperation{User: protocol.ToProtoUser(u)})); e != nil {
			t.Fatal(e)
		}
		c, _ := NewUDPPacketCodec(i.method, [][]byte{i.masterPSK, u.Account.(*MemoryAccount).Key})
		clients[j], _ = c.NewClientSession()
	}
	listed, err := rpc.GetInboundUsers(ctx, &command.GetInboundUserRequest{Tag: "ss"})
	if err != nil || len(listed.GetUsers()) != 2 {
		t.Fatalf("list users=%v err=%v", listed, err)
	}
	count, err := rpc.GetInboundUsersCount(ctx, &command.GetInboundUserRequest{Tag: "ss"})
	if err != nil || count.GetCount() != 2 {
		t.Fatalf("count=%v err=%v", count, err)
	}
	if err := safetyAlter(t, rpc, serial.ToTypedMessage(&command.AddUserOperation{User: protocol.ToProtoUser(users[0])})); err == nil {
		t.Fatal("duplicate email accepted")
	}
	duplicate := &protocol.MemoryUser{Email: "duplicate-key", Account: users[0].Account}
	if err := safetyAlter(t, rpc, serial.ToTypedMessage(&command.AddUserOperation{User: protocol.ToProtoUser(duplicate)})); err == nil {
		t.Fatal("duplicate key aliases another identity")
	}
	decode := func(packet []byte) (DecodedUDPPacket, *ServerUDPSession, error) {
		var raw [16]byte
		i.udpMasterCipher.Decrypt(raw[:], packet[:16])
		return i.decodeUDPPacket(binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:]), raw[:], packet)
	}
	packet := func(c *ClientUDPSession) []byte {
		b, e := c.EncodePacket(xnet.UDPDestination(xnet.LocalHostIP, 1234), []byte("payload"))
		if e != nil {
			t.Fatal(e)
		}
		defer b.Release()
		return append([]byte(nil), b.Bytes()...)
	}
	pa, pb := packet(clients[0]), packet(clients[1])
	_, sa, e := decode(pa)
	if e != nil {
		t.Fatal(e)
	}
	_, sb, e := decode(pb)
	if e != nil {
		t.Fatal(e)
	}
	defer sb.Close()
	var accepted atomic.Int64
	var wg sync.WaitGroup
	next := packet(clients[1])
	for j := 0; j < 32; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, e := decode(next); e == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("replay accepted %d times", accepted.Load())
	}
	if e := safetyAlter(t, rpc, serial.ToTypedMessage(&command.RemoveUserOperation{Email: "a"})); e != nil {
		t.Fatal(e)
	}
	if _, _, e := decode(packet(clients[0])); e == nil {
		t.Fatal("removed user accepted")
	}
	if _, _, e := decode(packet(clients[1])); e != nil {
		t.Fatalf("B affected: %v", e)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, e := sa.EnsureLink(ctx, a, xnet.UDPDestination(xnet.LocalHostIP, 1234), safetyDispatcher{}, i.policyManager, nil); e == nil {
		t.Fatal("revived removed link")
	}
	users[0] = &protocol.MemoryUser{Email: "a", Account: &MemoryAccount{Key: bytes.Repeat([]byte{4}, 16)}}
	if e := safetyAlter(t, rpc, serial.ToTypedMessage(&command.AddUserOperation{User: protocol.ToProtoUser(users[0])})); e != nil {
		t.Fatal(e)
	}
	if _, _, e := decode(packet(clients[0])); e == nil {
		t.Fatal("old key accepted after re-add")
	}
	c, _ := NewUDPPacketCodec(i.method, [][]byte{i.masterPSK, users[0].Account.(*MemoryAccount).Key})
	fresh, _ := c.NewClientSession()
	_, newA, e := decode(packet(fresh))
	if e != nil {
		t.Fatal(e)
	}
	// Inject a cleanup tick; no subsequent packet is needed to retire state.
	i.udpSessions.cleanup(time.Now().Add(2 * time.Minute).Unix())
	if n, w := sessionCounts(i.udpSessions); n != 0 || w != 0 {
		t.Fatalf("expired sessions=%d windows=%d", n, w)
	}
	if _, e := newA.EncodeServerPacket(i.method, fresh.ClientSessionID(), xnet.UDPDestination(xnet.LocalHostIP, 1234), nil); e == nil {
		t.Fatal("expired encoder revived")
	}
}

type safetyDispatcher struct {
	call func(context.Context, xnet.Destination) (*transport.Link, error)
}

func (d safetyDispatcher) Type() interface{} { return routing.DispatcherType() }
func (d safetyDispatcher) Start() error      { return nil }
func (d safetyDispatcher) Close() error      { return nil }
func (d safetyDispatcher) Dispatch(c context.Context, dst xnet.Destination) (*transport.Link, error) {
	return d.call(c, dst)
}
func (d safetyDispatcher) DispatchLink(context.Context, xnet.Destination, *transport.Link) error {
	return errors.New("unexpected DispatchLink")
}

func safetyMulti(t *testing.T) *MultiUserInbound {
	t.Helper()
	m, err := GetCipherMethod(MethodAES128GCM)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{1}, 16)
	b, err := m.NewBlock(key)
	if err != nil {
		t.Fatal(err)
	}
	return &MultiUserInbound{method: m, masterPSK: key, udpMasterCipher: b, policyManager: safetyPolicy{time.Second}, saltFilter: antireplay.NewMapFilter[[32]byte](60), udpSessions: NewUDPSessionManager(time.Minute), usersByHash: utils.NewTypedSyncMap[[16]byte, *protocol.MemoryUser](), usersByEmail: utils.NewTypedSyncMap[string, *protocol.MemoryUser]()}
}
func sessionCounts(m *UDPSessionManager) (int, int) {
	n, w := 0, 0
	m.sessions.Range(func(_ uint64, s *ServerUDPSession) bool {
		n++
		s.Lock()
		if s.Window != nil {
			w++
		}
		s.Unlock()
		return true
	})
	return n, w
}
func TestSafetyInvalidUDPAllocationWaves(t *testing.T) {
	m, _ := GetCipherMethod(MethodAES128GCM)
	key := bytes.Repeat([]byte{1}, 16)
	server, _ := NewUDPServerCodec(m, key, time.Minute)
	client, _ := NewUDPPacketCodec(m, [][]byte{key})
	for wave := 0; wave < 10; wave++ {
		for j := 0; j < 200; j++ {
			s, _ := client.NewClientSession()
			b, e := s.EncodePacket(xnet.UDPDestination(xnet.LocalHostIP, 1234), []byte("payload"))
			if e != nil {
				t.Fatal(e)
			}
			b.Bytes()[b.Len()-1] ^= 1
			_, e = server.DecodePacket(b.Bytes())
			b.Release()
			if e == nil {
				t.Fatal("invalid AEAD accepted")
			}
		}
		n, w := sessionCounts(server.sessions)
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		t.Logf("wave=%d sessions=%d windows=%d fd=%d heap_objects=%d", wave, n, w, fdSample(), mem.HeapObjects)
		if n != 0 || w != 0 {
			t.Fatalf("unauthenticated resources retained: sessions=%d windows=%d", n, w)
		}
	}
}

func TestSafetyHandshakeResourceWaves(t *testing.T) {
	multi := safetyMulti(t)
	single := &Inbound{method: multi.method, policyManager: multi.policyManager}
	relay := &RelayInbound{method: multi.method, policyManager: multi.policyManager}
	for name, process := range map[string]func(context.Context, xnet.Conn, routing.Dispatcher) error{"single": single.processTCP, "multi": multi.processTCP, "relay": relay.processTCP} {
		t.Run(name, func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			var active atomic.Int64
			d := safetyDispatcher{call: func(context.Context, xnet.Destination) (*transport.Link, error) {
				t.Error("unauthenticated dispatch")
				return nil, io.EOF
			}}
			for wave := 0; wave < 10; wave++ {
				var wg sync.WaitGroup
				clients := make([]net.Conn, 0, 20)
				var servers []*safetyConn
				for j := 0; j < 20; j++ {
					c, e := net.Dial("tcp", ln.Addr().String())
					if e != nil {
						t.Fatal(e)
					}
					clients = append(clients, c)
					raw, e := ln.Accept()
					if e != nil {
						t.Fatal(e)
					}
					s := &safetyConn{Conn: raw}
					servers = append(servers, s)
					active.Add(1)
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer active.Add(-1)
						if e := process(context.Background(), s, d); e == nil {
							t.Error("silent handshake accepted")
						}
					}()
				}
				for _, c := range clients {
					_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
					var b [1]byte
					_, e := c.Read(b[:])
					c.Close()
					if e == nil {
						t.Error("silent peer still readable")
					} else if ne, ok := e.(net.Error); ok && ne.Timeout() {
						t.Error("server did not close within budget")
					}
				}
				wg.Wait()
				for _, s := range servers {
					if s.closed.Load() != 1 {
						t.Errorf("accepted socket close count=%d", s.closed.Load())
					}
				}
				runtime.GC()
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				t.Logf("wave=%d owned_handlers=%d owned_sockets=%d fd=%d heap_objects=%d", wave, active.Load(), active.Load(), fdSample(), mem.HeapObjects)
				if active.Load() != 0 {
					t.Fatal("retained handler")
				}
			}
		})
	}
}

func TestSafetyUDPContextIsolation(t *testing.T) {
	parent := &session.Inbound{User: &protocol.MemoryUser{Email: "parent"}}
	ctx := session.ContextWithInbound(context.Background(), parent)
	var contexts []context.Context
	d := safetyDispatcher{call: func(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
		contexts = append(contexts, ctx)
		return nil, io.EOF
	}}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	for _, email := range []string{"a", "b"} {
		s := &ServerUDPSession{User: &protocol.MemoryUser{Email: email}}
		_, _ = s.EnsureLink(ctx, a, xnet.UDPDestination(xnet.LocalHostIP, 1234), d, safetyPolicy{time.Second}, nil)
	}
	if parent.User.Email != "parent" {
		t.Errorf("shared inbound mutated: %s", parent.User.Email)
	}
	for j, email := range []string{"a", "b"} {
		if got := session.InboundFromContext(contexts[j]).User.Email; got != email {
			t.Errorf("dispatch %d user=%s want=%s", j, got, email)
		}
	}
}
