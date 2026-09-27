package shadowsocks_2022

import (
	"context"
	"encoding/base64"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type failingConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *failingConn) Read([]byte) (int, error)  { return 0, io.ErrUnexpectedEOF }
func (c *failingConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (c *failingConn) Close() error              { c.closed.Store(true); return nil }

type resourceDialer struct {
	internet.Dialer
	conn *failingConn
}

func (d resourceDialer) Dial(context.Context, net.Destination) (stat.Connection, error) {
	return d.conn, nil
}

type failingReader struct{}

func (failingReader) ReadMultiBuffer() (buf.MultiBuffer, error) { return nil, io.ErrUnexpectedEOF }
func (failingReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return nil, io.ErrUnexpectedEOF
}

type discardWriter struct{}

func (discardWriter) WriteMultiBuffer(mb buf.MultiBuffer) error { buf.ReleaseMulti(mb); return nil }

func TestOutboundFailureClosesConnection(t *testing.T) {
	for _, network := range []net.Network{net.Network_TCP, net.Network_UDP} {
		for _, timeoutOnly := range []bool{false, true} {
			t.Run(network.String()+map[bool]string{false: "/normal", true: "/timeout-only"}[timeoutOnly], func(t *testing.T) {
				o, err := NewClient(context.Background(), &ClientConfig{Address: net.NewIPOrDomain(net.LocalHostIP), Port: 12345, Method: "2022-blake3-aes-128-gcm", Key: base64.StdEncoding.EncodeToString(make([]byte, 16))})
				if err != nil {
					t.Fatal(err)
				}
				ctx := session.ContextWithTimeoutOnly(context.Background(), timeoutOnly)
				ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: net.Destination{Network: network, Address: net.LocalHostIP, Port: 80}}})
				conn := new(failingConn)
				err = o.Process(ctx, &transport.Link{Reader: failingReader{}, Writer: discardWriter{}}, resourceDialer{conn: conn})
				if err == nil {
					t.Fatal("injected failure succeeded")
				}
				if !conn.closed.Load() {
					t.Fatal("outbound leaked connection on failure")
				}
			})
		}
	}
}
