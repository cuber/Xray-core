package outbound_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdnet "net"
	"os"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	outboundimpl "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

type contractOutbound struct {
	recordingHandler
	done chan error
}

func (h *contractOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	h.Process(ctx, link, nil)
}
func (h *contractOutbound) Process(ctx context.Context, link *transport.Link, _ internet.Dialer) error {
	defer common.Close(link.Writer)
	defer common.Interrupt(link.Reader)
	target := session.OutboundsFromContext(ctx)
	dest := target[len(target)-1].Target
	c, err := (&stdnet.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", dest.NetAddr())
	if err != nil {
		h.done <- err
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	up := make(chan error, 1)
	go func() { up <- buf.Copy(link.Reader, buf.NewWriter(c)); c.Close() }()
	err = buf.Copy(buf.NewReader(c), link.Writer)
	common.Interrupt(link.Reader)
	<-up
	h.done <- err
	return err
}

func TestDispatchEntrancesLifecycle(t *testing.T) {
	before, fdErr := os.ReadDir("/dev/fd")
	t.Cleanup(func() {
		after, err := os.ReadDir("/dev/fd")
		if fdErr == nil && err == nil {
			t.Logf("process FD diagnostic before=%d after=%d (runner and accepted connection joins are the ownership assertion)", len(before), len(after))
		} else {
			t.Logf("process FD diagnostic unavailable: before=%v after=%v; all owned runners and accepted connections joined", fdErr, err)
		}
	})
	listener, err := stdnet.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	early, err := stdnet.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	earlyDone := make(chan struct{})
	go func() {
		defer close(earlyDone)
		for {
			c, err := early.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	t.Cleanup(func() { early.Close(); <-earlyDone })
	refused, err := stdnet.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedAddr := refused.Addr().String()
	refused.Close()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	direct, err := stdnet.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	direct.SetDeadline(time.Now().Add(time.Second))
	direct.Write(payload)
	control := make([]byte, len(payload))
	_, err = io.ReadFull(direct, control)
	direct.Close()
	if err != nil || !bytes.Equal(control, payload) {
		t.Fatal("direct control failed", err)
	}
	manager, err := outboundimpl.New(context.Background(), &proxyman.OutboundConfig{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := core.New(&core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	v.AddFeature(manager)
	internet.InitSystemDialer(nil, manager)
	t.Cleanup(func() { internet.InitSystemDialer(nil, nil) })
	for _, entrance := range []string{"proxySettings", "dialerProxy", "singbridge"} {
		t.Run(entrance, func(t *testing.T) {
			t.Parallel()
			inner := &contractOutbound{recordingHandler: recordingHandler{tag: entrance}, done: make(chan error, 1)}
			if err := manager.AddHandler(context.Background(), inner); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), xrayKey, v)
			h, err := outboundimpl.NewHandler(ctx, &core.OutboundHandlerConfig{Tag: "outer", SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{ProxySettings: &internet.ProxyConfig{Tag: entrance}}), ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			for _, mode := range []string{"echo", "dial-failure", "early-close", "caller-close"} {
				t.Run(mode, func(t *testing.T) {
					for i := 0; i < 100; i++ {
						address := listener.Addr().String()
						if mode == "dial-failure" {
							address = refusedAddr
						}
						if mode == "early-close" {
							address = early.Addr().String()
						}
						addr := M.ParseSocksaddr(address)
						dest := net.TCPDestination(net.ParseAddress(addr.Addr.String()), net.Port(addr.Port))
						ctx, cancel := context.WithCancel(session.ContextWithOutbounds(ctx, []*session.Outbound{{}}))
						var c net.Conn
						switch entrance {
						case "proxySettings":
							c, err = h.(*outboundimpl.Handler).Dial(ctx, dest)
						case "dialerProxy":
							c, err = internet.DialSystem(ctx, dest, &internet.SocketConfig{DialerProxy: entrance})
						case "singbridge":
							c, err = singbridge.NewOutboundDialer(inner, nil).DialContext(ctx, "tcp", addr)
						}
						if err != nil {
							cancel()
							t.Fatal(err)
						}
						finished := make(chan error, 1)
						go func() {
							if mode == "echo" {
								if _, err := c.Write(payload); err != nil {
									finished <- err
									return
								}
								got := make([]byte, len(payload))
								_, err := io.ReadFull(c, got)
								if err == nil && !bytes.Equal(got, control) {
									err = fmt.Errorf("echo mismatch")
								}
								finished <- err
							} else {
								var b [1]byte
								_, err := c.Read(b[:])
								if err == nil {
									err = fmt.Errorf("expected closed read")
								} else {
									err = nil
								}
								finished <- err
							}
						}()
						if mode == "caller-close" {
							cancel()
							c.Close()
						}
						select {
						case err := <-finished:
							c.Close()
							cancel()
							if err != nil {
								t.Fatal(err)
							}
						case <-time.After(4 * time.Second):
							c.Close()
							cancel()
							t.Fatal("connection operation stuck")
						}
						select {
						case <-inner.done:
						case <-time.After(time.Second):
							t.Fatal("dispatch runner leaked")
						}
					}
				})
			}
		})
	}
}
