package shadowsocks_2022

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type safetyDatagram struct {
	net.Conn
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func newSafetyDatagram() *safetyDatagram {
	return &safetyDatagram{in: make(chan []byte, 1), out: make(chan []byte, 1), done: make(chan struct{})}
}

func (c *safetyDatagram) Read(p []byte) (int, error) {
	select {
	case b := <-c.in:
		return copy(p, b), nil
	case <-c.done:
		return 0, io.EOF
	}
}

func (c *safetyDatagram) Write(p []byte) (int, error) {
	select {
	case c.out <- append([]byte(nil), p...):
		return len(p), nil
	case <-c.done:
		return 0, io.ErrClosedPipe
	}
}
func (c *safetyDatagram) Close() error { c.once.Do(func() { close(c.done) }); return nil }
func (c *safetyDatagram) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
}

func TestSafetyUDPTransportIsolationAndRoamingWaves(t *testing.T) {
	i := safetyMulti(t)
	for j, email := range []string{"a", "b"} {
		if err := i.AddUser(context.Background(), &protocol.MemoryUser{Email: email, Account: &MemoryAccount{Key: bytes.Repeat([]byte{byte(j + 2)}, 16)}}); err != nil {
			t.Fatal(err)
		}
	}
	for wave := 0; wave < 10; wave++ {
		func() {
			conns := []*safetyDatagram{newSafetyDatagram(), newSafetyDatagram()}
			var processes sync.WaitGroup
			var mu sync.Mutex
			var saved []context.Context
			d := safetyDispatcher{call: func(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
				mu.Lock()
				saved = append(saved, ctx)
				mu.Unlock()
				r, w := pipe.New(pipe.WithSizeLimit(1 << 20))
				return &transport.Link{Reader: r, Writer: w}, nil
			}}
			for _, c := range conns {
				processes.Add(1)
				go func() {
					defer processes.Done()
					_ = i.Process(session.ContextWithInbound(context.Background(), &session.Inbound{}), xnet.Network_UDP, c, d)
				}()
			}
			defer func() {
				for _, c := range conns {
					c.Close()
				}
				processes.Wait()
			}()
			clients := make([]*ClientUDPSession, 2)
			for j := range clients {
				codec, _ := NewUDPPacketCodec(i.method, [][]byte{i.masterPSK, bytes.Repeat([]byte{byte(j + 2)}, 16)})
				clients[j], _ = codec.NewClientSession()
			}
			exchange := func(j int, c *safetyDatagram, n int) {
				payload := []byte(fmt.Sprintf("user=%d packet=%d", j, n))
				dst := xnet.UDPDestination(xnet.LocalHostIP, xnet.Port(1234+n%2))
				b, err := clients[j].EncodePacket(dst, payload)
				if err != nil {
					t.Error(err)
					return
				}
				wire := append([]byte(nil), b.Bytes()...)
				b.Release()
				select {
				case c.in <- wire:
				case <-time.After(time.Second):
					t.Error("send blocked")
					return
				}
				select {
				case reply := <-c.out:
					p, err := clients[j].DecodePacket(reply)
					if err != nil || !bytes.Equal(p.Payload, payload) || p.Destination != dst {
						t.Errorf("user=%d response=%+v err=%v", j, p, err)
					}
				case <-time.After(time.Second):
					t.Error("reply missing")
				}
			}
			// Both authenticated identities initially share one inbound transport.
			exchange(0, conns[0], 0)
			exchange(1, conns[0], 0)
			var wg sync.WaitGroup
			for j := range clients {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for n := 1; n <= 20; n++ {
						exchange(j, conns[1-j], n)
					}
				}()
			}
			wg.Wait()
			mu.Lock()
			if len(saved) != 2 {
				t.Errorf("dispatch count=%d", len(saved))
			}
			for j, ctx := range saved {
				if got := session.InboundFromContext(ctx).User.Email; got != []string{"a", "b"}[j] {
					t.Errorf("context %d=%s", j, got)
				}
			}
			mu.Unlock()
			for _, client := range clients {
				s, ok := i.udpSessions.sessions.Load(client.ClientSessionID())
				if !ok {
					t.Error("active session missing")
					continue
				}
				s.Lock()
				done := s.done
				s.Unlock()
				s.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("response worker retained")
				}
			}
		}()
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		n, w := sessionCounts(i.udpSessions)
		t.Logf("wave=%d sessions=%d windows=%d joined_processes=2 joined_replies=2 fd=%d heap_objects=%d", wave, n, w, fdSample(), mem.HeapObjects)
		if n != 0 || w != 0 {
			t.Fatal("owned UDP resources retained")
		}
	}
}

func TestSafetyUDPInvalidEIHAndAEADWaves(t *testing.T) {
	i := safetyMulti(t)
	key := bytes.Repeat([]byte{2}, 16)
	if err := i.AddUser(context.Background(), &protocol.MemoryUser{Email: "a", Account: &MemoryAccount{Key: key}}); err != nil {
		t.Fatal(err)
	}
	d := safetyDispatcher{call: func(context.Context, xnet.Destination) (*transport.Link, error) {
		t.Error("invalid packet dispatched")
		return nil, io.EOF
	}}
	codec, _ := NewUDPPacketCodec(i.method, [][]byte{i.masterPSK, key})
	for wave := 0; wave < 10; wave++ {
		c := newSafetyDatagram()
		done := make(chan struct{})
		go func() { defer close(done); _ = i.processUDP(context.Background(), c, d) }()
		for n := 0; n < 200; n++ {
			client, _ := codec.NewClientSession()
			b, _ := client.EncodePacket(xnet.UDPDestination(xnet.LocalHostIP, 1234), []byte("invalid"))
			wire := append([]byte(nil), b.Bytes()...)
			b.Release()
			if n%2 == 0 {
				wire[16] ^= 1
			} else {
				wire[len(wire)-1] ^= 1
			}
			c.in <- wire
		}
		// Close after the input channel has drained, using a sentinel read barrier.
		c.in <- []byte{0}
		c.Close()
		<-done
		n, w := sessionCounts(i.udpSessions)
		if n != 0 || w != 0 {
			t.Fatalf("wave=%d sessions=%d windows=%d", wave, n, w)
		}
	}
}

func TestSafetyUDPCloseDuringDispatch(t *testing.T) {
	s := &ServerUDPSession{}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	d := safetyDispatcher{call: func(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
		close(entered)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return nil, io.EOF
	}}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_, _ = s.EnsureLink(context.Background(), a, xnet.UDPDestination(xnet.LocalHostIP, 1234), d, safetyPolicy{time.Second}, nil)
	}()
	<-entered
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		close(release)
		<-closed
		<-returned
		t.Fatal("Close blocked behind Dispatch instead of cancelling it")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(release)
		<-returned
		t.Fatal("dispatch did not observe session cancellation")
	}
}

func TestSafetyUDPExpiryWithoutTrafficAndManagerClose(t *testing.T) {
	m, _ := GetCipherMethod(MethodAES128GCM)
	key := bytes.Repeat([]byte{1}, 16)
	server, _ := NewUDPServerCodec(m, key, 20*time.Millisecond)
	defer server.sessions.Close()
	client, _ := NewUDPPacketCodec(m, [][]byte{key})
	c, _ := client.NewClientSession()
	b, _ := c.EncodePacket(xnet.UDPDestination(xnet.LocalHostIP, 1234), []byte("expiry"))
	defer b.Release()
	p, err := server.DecodePacket(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s := server.GetSession(p.SessionID)
	if s == nil {
		t.Fatal("authenticated state missing")
	}
	deadline := time.Now().Add(time.Second)
	for {
		n, _ := sessionCounts(server.sessions)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle session requires another packet to expire")
		}
		time.Sleep(time.Millisecond)
	}
	if err := server.sessions.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := server.DecodePacket(b.Bytes()); err == nil {
		t.Fatal("closed manager recreated state")
	}
	if err := s.EnsureServerState(m, key); err == nil {
		t.Fatal("closed session recreated cipher state")
	}
}

func TestSafetyUDPActiveRPCRevocation(t *testing.T) {
	i := safetyMulti(t)
	defer i.Close()
	rpc := safetyRPC(t, i)
	keyA, keyB := bytes.Repeat([]byte{2}, 16), bytes.Repeat([]byte{3}, 16)
	add := func(name string, key []byte) {
		t.Helper()
		u := &protocol.MemoryUser{Email: name, Account: &MemoryAccount{Key: key}}
		if err := safetyAlter(t, rpc, serial.ToTypedMessage(&command.AddUserOperation{User: protocol.ToProtoUser(u)})); err != nil {
			t.Fatal(err)
		}
	}
	add("a", keyA)
	add("b", keyB)
	client := func(key []byte) *ClientUDPSession {
		c, err := NewUDPPacketCodec(i.method, [][]byte{i.masterPSK, key})
		if err != nil {
			t.Fatal(err)
		}
		s, err := c.NewClientSession()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := client(keyA), client(keyB)
	c := newSafetyDatagram()
	processDone := make(chan struct{})
	d := safetyDispatcher{call: func(context.Context, xnet.Destination) (*transport.Link, error) {
		r, w := pipe.New(pipe.WithSizeLimit(1 << 20))
		return &transport.Link{Reader: r, Writer: w}, nil
	}}
	go func() {
		defer close(processDone)
		_ = i.Process(session.ContextWithInbound(context.Background(), &session.Inbound{}), xnet.Network_UDP, c, d)
	}()
	defer func() { c.Close(); <-processDone }()
	send := func(s *ClientUDPSession, text string) {
		t.Helper()
		pkt, err := s.EncodePacket(xnet.UDPDestination(xnet.LocalHostIP, 1234), []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		wire := append([]byte(nil), pkt.Bytes()...)
		pkt.Release()
		select {
		case c.in <- wire:
		case <-time.After(time.Second):
			t.Fatal("UDP input blocked")
		}
	}
	receive := func(s *ClientUDPSession, text string) {
		t.Helper()
		select {
		case wire := <-c.out:
			p, err := s.DecodePacket(wire)
			if err != nil || string(p.Payload) != text {
				t.Fatalf("reply=%q want=%q err=%v", p.Payload, text, err)
			}
		case <-time.After(time.Second):
			t.Fatal("UDP reply missing")
		}
	}
	join := func(s *ServerUDPSession) {
		t.Helper()
		s.Lock()
		done := s.done
		s.Unlock()
		s.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("removed UDP worker did not exit")
		}
	}
	send(a, "a-before")
	receive(a, "a-before")
	send(b, "b-before")
	receive(b, "b-before")
	sa, _ := i.udpSessions.sessions.Load(a.ClientSessionID())
	sb, _ := i.udpSessions.sessions.Load(b.ClientSessionID())
	if sa == nil || sb == nil {
		t.Fatal("active sessions missing")
	}
	if err := safetyAlter(t, rpc, serial.ToTypedMessage(&command.RemoveUserOperation{Email: "a"})); err != nil {
		t.Fatal(err)
	}
	join(sa)
	send(a, "old-a-after-remove")
	send(b, "b-after-remove")
	receive(b, "b-after-remove")
	newKey := bytes.Repeat([]byte{4}, 16)
	add("a", newKey)
	fresh := client(newKey)
	send(a, "old-a-after-readd")
	send(b, "b-after-readd")
	receive(b, "b-after-readd")
	send(fresh, "new-a")
	receive(fresh, "new-a")
	newA, ok := i.udpSessions.sessions.Load(fresh.ClientSessionID())
	if !ok {
		t.Fatal("new key session missing")
	}
	if n, _ := sessionCounts(i.udpSessions); n != 2 {
		t.Fatalf("unexpected retained sessions=%d", n)
	}
	join(newA)
	join(sb)
	if n, w := sessionCounts(i.udpSessions); n != 0 || w != 0 {
		t.Fatalf("after drain sessions=%d windows=%d", n, w)
	}
}
