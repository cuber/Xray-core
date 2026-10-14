package freedom

import (
	"context"
	"errors"
	stdnet "net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
)

type unixContractDialer struct{ destinations chan net.Destination }

func (d unixContractDialer) Dial(ctx context.Context, dest net.Destination) (stat.Connection, error) {
	d.destinations <- dest
	return internet.DialSystem(ctx, dest, nil)
}
func (unixContractDialer) DestIpAddress() net.IP                                 { return nil }
func (unixContractDialer) SetOutboundGateway(context.Context, *session.Outbound) {}

func TestFreedomUnixContractMissingClosedCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS fixture requires POSIX socket paths")
	}
	// Do not inherit long TMPDIR paths: sockaddr_un has a small fixed path limit.
	dir, err := os.MkdirTemp("/tmp", "xray-uds-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	for _, mode := range []string{"missing", "closed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			path := dir + "/" + mode
			if mode != "missing" {
				l, err := stdnet.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "closed" {
					l.Close()
				} else {
					defer l.Close()
				}
			}
			h := new(Handler)
			if err := h.Init(&Config{DestinationOverride: &DestinationOverride{Network: net.Network_UNIX, Server: &protocol.ServerEndpoint{Address: net.NewIPOrDomain(net.DomainAddress(path))}}}, testPolicyManager{}); err != nil {
				t.Fatal(err)
			}
			request, writer := pipe.New()
			defer request.Interrupt()
			defer writer.Close()
			response, output := pipe.New()
			defer response.Interrupt()
			defer output.Close()
			ctx, cancel := context.WithCancel(session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Target: net.TCPDestination(net.LocalHostIP, 80)}}))
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			d := unixContractDialer{destinations: make(chan net.Destination, 8)}
			done := make(chan error, 1)
			go func() { done <- h.Process(ctx, &transport.Link{Reader: request, Writer: output}, d) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("failed Unix destination returned success")
				}
				if mode == "canceled" && !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("context was not canceled")
				}
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("Freedom Unix failure did not return")
			}
			close(d.destinations)
			count := 0
			for dest := range d.destinations {
				count++
				if dest.Network != net.Network_UNIX || dest.Address.String() != path {
					t.Fatalf("redirect fell back to wrong destination: %v", dest)
				}
			}
			if count == 0 {
				t.Fatal("no Unix dial attempted")
			}
		})
	}
}
