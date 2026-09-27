package anytls_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/anytls"
	"google.golang.org/protobuf/proto"
)

// The peer consumes a complete ClientHello record but never sends ServerHello.
// Its EOF barrier measures the actual socket, including the forwarded socket
// on chained paths, rather than treating a canceled caller as transport cleanup.
func retirementSilentTLS(t *testing.T) (uint32, <-chan error, <-chan struct{}) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hello := make(chan error, 1)
	done := make(chan struct{})
	var mu sync.Mutex
	var socket net.Conn
	stopping := false
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			hello <- err
			return
		}
		defer c.Close()
		mu.Lock()
		socket = c
		if stopping {
			c.Close()
		}
		mu.Unlock()
		var header [5]byte
		if _, err := io.ReadFull(c, header[:]); err != nil {
			hello <- err
			return
		}
		record := make([]byte, int(binary.BigEndian.Uint16(header[3:])))
		if _, err := io.ReadFull(c, record); err != nil {
			hello <- err
			return
		}
		if header[0] != 22 || len(record) < 4 || record[0] != 1 {
			hello <- fmt.Errorf("expected ClientHello, record type=%d length=%d", header[0], len(record))
			return
		}
		hello <- nil
		io.Copy(io.Discard, c)
	}()
	t.Cleanup(func() {
		l.Close()
		mu.Lock()
		stopping = true
		if socket != nil {
			socket.Close()
		}
		mu.Unlock()
		obCountsWait(t, done, "silent TLS peer worker")
	})
	return uint32(l.Addr().(*net.TCPAddr).Port), hello, done
}

func TestAnyTLSOutboundRetirementDuringTLS(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/anytls", "dialerProxy/anytls"} {
		t.Run("chain="+mode, func(t *testing.T) {
			f, good := outboundFixture(t, mode)
			port, hello, peerDone := retirementSilentTLS(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			blocked := proto.Clone(good).(*core.OutboundHandlerConfig)
			value, err := blocked.ProxySettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			value.(*anytls.ClientConfig).Server.Port = port
			blocked.ProxySettings = serial.ToTypedMessage(value)
			remove := func() {
				t.Helper()
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
					t.Fatal(err)
				}
			}
			add := func(config *core.OutboundHandlerConfig) {
				t.Helper()
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
					t.Fatal(err)
				}
			}
			remove()
			add(blocked)
			manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
			old := manager.GetHandler("client")
			observed := &chainCountsAlias{Handler: old, tag: "retirement-observer", done: make(chan struct{}, 1)}
			if err := manager.AddHandler(ctx, observed); err != nil {
				t.Fatal(err)
			}
			dispatcher := f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
			link, err := dispatcher.Dispatch(session.SetForcedOutboundTagToContext(ctx, observed.Tag()), xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80))
			if err != nil {
				t.Fatal(err)
			}
			defer common.Interrupt(link.Reader)
			defer common.Interrupt(link.Writer)
			select {
			case err := <-hello:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("no ClientHello", ctx.Err())
			}
			if observed.closed.Load() != 0 {
				t.Fatal("native dispatch ended before removal")
			}
			// One shared bound is shorter than the fixture's 2s handshake budget.
			// No caller cancellation or local socket close can satisfy these joins.
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			remove()
			add(good)
			wait := func(name string, done <-chan struct{}) {
				t.Helper()
				select {
				case <-done:
				case <-deadline.C:
					t.Fatalf("RemoveOutbound did not finish %s before handshake timeout: dispatch=%d/%d ctx=%v", name, observed.opened.Load(), observed.closed.Load(), ctx.Err())
				}
			}
			wait("native dispatch", observed.done)
			// Only inspect the idempotent retirement completion after gRPC alone
			// has caused dispatch to exit; do not manufacture cancellation here.
			wait("retired pool", old.(outbound.RetiringHandler).Retirement().Retire())
			// A chained server preserves the configured downlink-only window
			// after stream EOF (1s in this fixture). This separate cleanup join
			// cannot substitute for the prompt local cancellation asserted above.
			obCountsWait(t, peerDone, "silent TLS peer socket")
			if observed.opened.Load() != 1 || observed.closed.Load() != 1 || ctx.Err() != nil {
				t.Fatalf("dispatch counts=%d/%d ctx=%v", observed.opened.Load(), observed.closed.Load(), ctx.Err())
			}
			response, err := link.Reader.(buf.TimeoutReader).ReadMultiBufferTimeout(time.Second)
			defer buf.ReleaseMulti(response)
			if err == nil || err == buf.ErrReadTimeout || !response.IsEmpty() {
				t.Fatalf("retired pending dispatch did not terminate: %v buffers=%d", err, len(response))
			}
			replacement := manager.GetHandler("client")
			business, err := core.Dial(session.SetForcedOutboundTagToContext(ctx, "client"), f.instance, xnet.TCPDestination(xnet.DomainAddress("alpha.test"), 80))
			if err != nil {
				t.Fatal(err)
			}
			defer business.Close()
			business.SetDeadline(time.Now().Add(3 * time.Second))
			round := func(payload, want string) {
				t.Helper()
				if _, err := io.WriteString(business, payload); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(business, got); err != nil || string(got) != want {
					t.Fatalf("replacement response=%q want=%q err=%v", got, want, err)
				}
			}
			round("before", "Abefore")
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			if replacement == old || manager.GetHandler("client") != replacement {
				t.Fatal("old cleanup changed replacement registration")
			}
			round("after", "after")
			t.Log("ClientHello=1; pending native dispatch=1/1; peer EOF and retirement joined; replacement survived old Close")
		})
	}
}
