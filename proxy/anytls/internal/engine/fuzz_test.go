package anytls

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type discardHandler struct{}

func (discardHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	conn.Close()
}

type finiteConn struct{ *bytes.Reader }

type fragmentedConn struct {
	*finiteConn
	chunk int
}

func (c *fragmentedConn) Read(p []byte) (int, error) {
	if len(p) > c.chunk {
		p = p[:c.chunk]
	}
	return c.finiteConn.Read(p)
}

func FuzzAuthenticationPrologue(f *testing.F) {
	digest := sha256.Sum256([]byte("fuzz-only-account"))
	f.Add([]byte{}, uint8(1))
	f.Add(make([]byte, 34), uint8(7))
	f.Add(append(append([]byte{}, digest[:]...), 0, 0), uint8(1))
	f.Add(append(append([]byte{}, digest[:]...), 255, 255), uint8(32))
	f.Add(append(append([]byte{}, digest[:]...), 0, 2, 1, 2), uint8(3))
	f.Fuzz(func(t *testing.T, input []byte, chunk uint8) {
		if len(input) > 65570 {
			return
		}
		var calls, ready, closed int
		var streams atomic.Int32
		s, err := NewService("unused", ServiceOptions{
			Handler: discardHandler{},
			Authenticate: func(ctx context.Context, password []byte) (context.Context, bool) {
				calls++
				return ctx, bytes.Equal(password, digest[:])
			},
			SessionReady:  func(context.Context) { ready++ },
			SessionClosed: func(context.Context) { closed++ },
			StreamOpen: func(context.Context) bool {
				if streams.Add(1) > 32 {
					streams.Add(-1)
					return false
				}
				return true
			},
			StreamClose:      func(context.Context) { streams.Add(-1) },
			HandshakeTimeout: func(context.Context) time.Duration { return time.Millisecond },
		})
		if err != nil {
			t.Fatal(err)
		}
		conn := &fragmentedConn{finiteConn: &finiteConn{bytes.NewReader(input)}, chunk: int(chunk) + 1}
		_ = s.NewConnection(context.Background(), conn, M.Socksaddr{}, nil)
		wantCalls, wantReady := 0, 0
		if len(input) >= 32 {
			wantCalls = 1
		}
		if len(input) >= 34 && bytes.Equal(input[:32], digest[:]) && len(input)-34 >= int(binary.BigEndian.Uint16(input[32:34])) {
			wantReady = 1
		}
		if calls != wantCalls || ready != wantReady || closed != wantReady || streams.Load() != 0 {
			t.Fatalf("calls=%d/%d ready=%d/%d closed=%d streams=%d", calls, wantCalls, ready, wantReady, closed, streams.Load())
		}
	})
}

func (c *finiteConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *finiteConn) Close() error                     { return nil }
func (c *finiteConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *finiteConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *finiteConn) SetDeadline(time.Time) error      { return nil }
func (c *finiteConn) SetReadDeadline(time.Time) error  { return nil }
func (c *finiteConn) SetWriteDeadline(time.Time) error { return nil }

func FuzzServerFrames(f *testing.F) {
	f.Add([]byte{4, 0, 0, 0, 0, 0, 4, 'v', '=', '2', '\n', 1, 0, 0, 0, 1, 0, 0, 2, 0, 0, 0, 1, 0, 3, 3, 1, 'x'})
	f.Add([]byte{2, 0, 0, 0, 1, 255, 255})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 65536 {
			return
		}
		var active atomic.Int32
		s, err := NewService("unused", ServiceOptions{
			Handler:          discardHandler{},
			Authenticate:     func(ctx context.Context, _ []byte) (context.Context, bool) { return ctx, true },
			StreamOpen:       func(context.Context) bool { return active.Add(1) <= 32 },
			HandshakeTimeout: func(context.Context) time.Duration { return time.Millisecond },
		})
		if err != nil {
			t.Fatal(err)
		}
		request := append(make([]byte, 34), input...)
		conn := &finiteConn{Reader: bytes.NewReader(request)}
		_ = s.NewConnection(context.Background(), conn, M.Socksaddr{}, nil)
		_, _ = io.Copy(io.Discard, conn)
	})
}
