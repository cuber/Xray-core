package anytls

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
)

type finiteUDPConn struct{ *bytes.Reader }

func (c *finiteUDPConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *finiteUDPConn) Close() error                     { return nil }
func (c *finiteUDPConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *finiteUDPConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *finiteUDPConn) SetDeadline(time.Time) error      { return nil }
func (c *finiteUDPConn) SetReadDeadline(time.Time) error  { return nil }
func (c *finiteUDPConn) SetWriteDeadline(time.Time) error { return nil }

func FuzzUoTAdapter(f *testing.F) {
	var request bytes.Buffer
	_ = uot.WriteRequest(&request, uot.Request{Destination: M.ParseSocksaddr("127.0.0.1:53")})
	f.Add(request.Bytes())
	f.Add([]byte{0, 3, 255, 0})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 65536 {
			return
		}
		s, err := newServer(&ServerConfig{}, policy.DefaultManager{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		u, _ := testUser("fuzz", "fuzz-secret").ToMemoryUser()
		ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: u})
		// Any valid target fails dispatch deterministically; malformed headers
		// and payload lengths still exercise the complete adapter parser.
		ctx = session.ContextWithDispatcher(ctx, &budgetDispatcher{fail: true})
		_ = s.serveUDP(ctx, &finiteUDPConn{bytes.NewReader(input)})
		if s.activeUDPLinks != 0 {
			t.Fatal("fuzz input leaked admission credit")
		}
	})
}
