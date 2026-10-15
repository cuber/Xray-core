package anytls

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

const finReply = "delivered-before-physical-write-returns"

type deliveredReplyConn struct {
	net.Conn
	delivered, resume, closed chan struct{}
	once                      sync.Once
}

func (c *deliveredReplyConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && bytes.Contains(p, []byte(finReply)) {
		close(c.delivered)
		select {
		case <-c.resume:
		case <-c.closed:
			return n, net.ErrClosed
		}
	}
	return n, err
}

func (c *deliveredReplyConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type finBusinessHandler struct {
	server *Server
	udp    bool
	done   chan error
}

func (h finBusinessHandler) NewConnectionEx(ctx context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	u, _ := testUser("fin", "secret").ToMemoryUser()
	ctx = session.ContextWithInbound(ctx, &session.Inbound{User: u})
	ctx = session.ContextWithDispatcher(ctx, &budgetDispatcher{reader: buf.NewReader(bytes.NewReader([]byte(finReply)))})
	if h.udp {
		N.ReportHandshakeSuccess(conn)
		h.done <- h.server.serveUDP(ctx, conn)
	} else {
		h.done <- h.server.copyTCP(ctx, conn, xnet.TCPDestination(xnet.LocalHostIP, 80))
	}
}

func TestBusinessPeerFINJoinsDeliveredWrite(t *testing.T) {
	for _, mode := range []string{"tcp-complete", "uot-complete", "tcp-deadline", "uot-deadline", "tcp-parent-cancel", "uot-parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			udp := mode[:3] == "uot"
			s, err := newServer(&ServerConfig{}, policy.DefaultManager{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, b := net.Pipe()
			physical := &deliveredReplyConn{Conn: a, delivered: make(chan struct{}), resume: make(chan struct{}), closed: make(chan struct{})}
			done := make(chan error, 1)
			service, err := engine.NewService("secret", engine.ServiceOptions{Handler: finBusinessHandler{s, udp, done}})
			if err != nil {
				t.Fatal(err)
			}
			loop := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { defer close(loop); service.NewConnection(ctx, physical, M.Socksaddr{}, nil) }()
			client, err := engine.NewClient(engine.ClientOptions{Password: "secret", CancelableWrites: true, DialOut: func(context.Context) (net.Conn, error) { return b, nil }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { client.Close(); physical.Close(); <-loop })
			conn, err := client.OpenContext(context.Background(), M.ParseSocksaddr("target.test:80"))
			if err != nil {
				t.Fatal(err)
			}
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			if udp {
				req := uot.Request{Destination: M.ParseSocksaddr("target.test:53")}
				if err := uot.WriteRequest(conn, req); err != nil {
					t.Fatal(err)
				}
				packets := uot.NewConn(conn, req)
				if err := packets.WritePacket(B.As([]byte("request")), req.Destination); err != nil {
					t.Fatal(err)
				}
				response := B.NewSize(1024)
				defer response.Release()
				if _, err := packets.ReadPacket(response); err != nil || string(response.Bytes()) != finReply {
					t.Fatalf("reply=%q err=%v", response.Bytes(), err)
				}
			} else {
				response := make([]byte, len(finReply))
				if _, err := io.ReadFull(conn, response); err != nil || string(response) != finReply {
					t.Fatalf("reply=%q err=%v", response, err)
				}
			}
			<-physical.delivered
			conn.Close()
			select {
			case <-physical.closed:
				t.Fatal("business EOF cleanup canceled delivered response and physical session")
			case <-time.After(50 * time.Millisecond):
			}
			switch mode[4:] {
			case "complete":
				close(physical.resume)
			case "parent-cancel":
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("business worker did not join completed write")
			}
			if mode[4:] != "complete" {
				select {
				case <-physical.closed:
				default:
					t.Fatal("deadline/cancellation did not close blocked physical writer")
				}
			}
		})
	}
}
