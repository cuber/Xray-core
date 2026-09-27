package anytls

import (
	"context"
	"net"
	"os"
	"runtime/debug"
	"sync/atomic"
	"testing"
)

type reuseDiagnosticConn struct {
	net.Conn
	t       *testing.T
	writing atomic.Int32
}

func (c *reuseDiagnosticConn) Write(p []byte) (int, error) {
	c.writing.Add(1)
	defer c.writing.Add(-1)
	return c.Conn.Write(p)
}

func (c *reuseDiagnosticConn) Close() error {
	c.t.Logf("physical Close activeWrites=%d: %s", c.writing.Load(), debug.Stack())
	return c.Conn.Close()
}

func TestReuseCloseDiagnostic(t *testing.T) {
	if os.Getenv("ANYTLS_REUSE_DIAGNOSTIC") != "1" {
		t.Skip("set ANYTLS_REUSE_DIAGNOSTIC=1 to capture physical close stacks")
	}
	c, dialer, count := clientFixture(t)
	dial := dialer.dial
	dialer.dial = func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		return &reuseDiagnosticConn{Conn: conn, t: t}, nil
	}
	for range 100 {
		conn, stop := clientStream(t, c, dialer)
		err := clientExchange(conn, make([]byte, 65536))
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("physical=%d", count.Load())
	}
}
