package hysteria

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apernet/quic-go"
)

// Core spec026 R3: a control-plane close of a Hy2 stream releases a Write that waits for the peer's flow control.
// The 2026-11-10 Akile gate found a relayed user's stream left blocked in Write after its close: Close only queues a
// FIN behind the unsent data, and a peer that stops reading never takes it.
func TestAbortReleasesAWriteBlockedOnFlowControl(t *testing.T) {
	cert := httptest.NewTLSServer(nil)
	serverTLS := cert.TLS.Clone()
	serverTLS.NextProtos = []string{"h3"}
	roots := x509.NewCertPool()
	roots.AddCert(cert.Certificate())
	cert.Close()
	small := &quic.Config{InitialStreamReceiveWindow: 64 << 10, MaxStreamReceiveWindow: 64 << 10}
	ql, err := quic.ListenAddr("127.0.0.1:0", serverTLS, small)
	if err != nil {
		t.Fatal(err)
	}
	defer ql.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := quic.DialAddr(ctx, ql.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "example.com", NextProtos: []string{"h3"}}, small)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseWithError(0, "")
	server, err := ql.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.CloseWithError(0, "")
	clientStream, err := client.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientStream.Write([]byte("x")); err != nil { // the server accepts a stream once data arrives
		t.Fatal(err)
	}
	stream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := &interConn{stream: stream, local: server.LocalAddr(), remote: server.RemoteAddr()}

	// The client never reads: the server's writes fill the stream window and block.
	var written int
	progress := make(chan int, 1024)
	done := make(chan error, 1)
	go func() {
		chunk := make([]byte, 16<<10)
		for {
			if _, err := c.Write(chunk); err != nil {
				done <- err
				return
			}
			written += len(chunk)
			select {
			case progress <- written:
			default:
			}
		}
	}()
	for idle := time.NewTimer(500 * time.Millisecond); ; {
		select {
		case <-progress:
			idle.Reset(500 * time.Millisecond)
			continue
		case err := <-done:
			t.Fatalf("write ended before the window filled: %v", err)
		case <-idle.C:
		}
		break
	}

	// The stream's ordinary close leaves the write blocked; the abort releases it.
	c.Close()
	select {
	case err := <-done:
		t.Logf("Close released the write (%v); the abort is still required for a peer that never reads", err)
	case <-time.After(300 * time.Millisecond):
	}
	start := time.Now()
	c.Abort()
	select {
	case <-done:
		t.Logf("released %s after the abort", time.Since(start))
	case <-time.After(2 * time.Second):
		t.Fatal("Abort left the write blocked")
	}
}
