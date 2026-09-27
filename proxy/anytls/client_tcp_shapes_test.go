package anytls

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type tcpShapeHandler func(net.Conn)

func (h tcpShapeHandler) NewConnectionEx(_ context.Context, conn net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer conn.Close()
	if N.ReportHandshakeSuccess(conn) == nil {
		h(conn)
	}
}

type tcpShapeReader struct {
	*bytes.Reader
	chunk int
	eof   chan struct{}
	once  sync.Once
}

func (r *tcpShapeReader) Read(p []byte) (int, error) {
	if len(p) > r.chunk {
		p = p[:r.chunk]
	}
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.once.Do(func() { close(r.eof) })
	}
	return n, err
}

type tcpShapeDownload struct {
	io.Reader
	chunk int
}

func (r tcpShapeDownload) Read(p []byte) (int, error) {
	if len(p) > r.chunk {
		p = p[:r.chunk]
	}
	return r.Reader.Read(p)
}

func TestClientTCPDelayedResponseAfterLocalEOF(t *testing.T) {
	for _, shape := range []struct {
		name                       string
		upload, chunk, reply, read int
	}{
		{"small-writes-large-response", 137, 7, 65536, 131072},
		{"large-upload-small-response", 1024 * 1024, 65536, 17, 3},
	} {
		t.Run(shape.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{0x37}, shape.upload)
			reply := bytes.Repeat([]byte{0xa9}, shape.reply)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			serverDone := make(chan error, 1)
			c, dialer, _ := clientFixture(t, tcpShapeHandler(func(conn net.Conn) {
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, got); err != nil {
					serverDone <- err
					return
				}
				if !bytes.Equal(got, payload) {
					serverDone <- fmt.Errorf("upload mismatch")
					return
				}
				// Local upload EOF does not imply a remote TCP half-close.
				// The controller releases the response only after observing EOF.
				<-release
				_, err := conn.Write(reply)
				serverDone <- err
			}))
			defer unblock()
			input := &tcpShapeReader{Reader: bytes.NewReader(payload), chunk: shape.chunk, eof: make(chan struct{})}
			reader, writer := pipe.New(pipe.WithSizeLimit(2 * 1024 * 1024))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress("shape.test"), 80)}})
			done := make(chan error, 1)
			workerEnded := make(chan struct{})
			go func() {
				defer close(workerEnded)
				done <- c.Process(ctx, &transport.Link{Reader: buf.NewReader(input), Writer: writer}, dialer)
			}()
			t.Cleanup(func() {
				unblock()
				cancel()
				c.Close()
				select {
				case <-workerEnded:
				case <-time.After(5 * time.Second):
					t.Error("Process worker did not join")
				}
			})
			select {
			case <-input.eof:
			case <-ctx.Done():
				t.Fatal("upload did not reach local EOF")
			}
			select {
			case err := <-done:
				t.Fatalf("Process ended before final response: %v", err)
			default:
			}
			unblock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("Process failed to finish after response")
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("server response worker did not finish")
			}
			got, err := io.ReadAll(tcpShapeDownload{Reader: &buf.BufferedReader{Reader: reader}, chunk: shape.read})
			if err != nil || !bytes.Equal(got, reply) {
				t.Fatalf("response bytes=%d want=%d err=%v", len(got), len(reply), err)
			}
		})
	}
}

func TestClientTCPServerFirst(t *testing.T) {
	banner := []byte("server-first-banner")
	payload := []byte("client-after-banner")
	c, dialer, _ := clientFixture(t, tcpShapeHandler(func(conn net.Conn) {
		if _, err := conn.Write(banner); err == nil {
			io.Copy(conn, conn)
		}
	}))
	uploaded := make(chan struct{})
	conn, stop := clientStream(t, c, dialer, func(reader buf.Reader) buf.Reader {
		return &uploadedReader{Reader: reader, remaining: int32(len(payload)), done: uploaded}
	})
	defer stop()
	got := make([]byte, len(banner))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, banner) {
		t.Fatalf("banner before business upload: %q err=%v", got, err)
	}
	if err := clientExchange(conn, payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-uploaded:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not complete")
	}
}
