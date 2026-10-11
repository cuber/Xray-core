package burst

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func http2FixtureClient(t *testing.T, ctx context.Context, server *httptest.Server, timeout time.Duration, keepAlive, http2 bool) *pingClient {
	t.Helper()
	client := newPingClient(ctx, nil, server.URL+"/generate_204?ob=hkt", timeout, "probe", keepAlive, http2)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.httpClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestHTTP2ProbeNegotiationAndReuse(t *testing.T) {
	contractLoopbackDialer(t)
	for _, tc := range []struct {
		name      string
		serverH2  bool
		http2     bool
		keepAlive bool
		wantProto int
	}{
		{name: "default_http1", serverH2: true, wantProto: 1},
		{name: "http1_keepalive", serverH2: true, keepAlive: true, wantProto: 1},
		{name: "http2_short_connections", serverH2: true, http2: true, wantProto: 2},
		{name: "http2_keepalive", serverH2: true, http2: true, keepAlive: true, wantProto: 2},
		{name: "http1_fallback", http2: true, wantProto: 1},
		{name: "http1_fallback_keepalive", http2: true, keepAlive: true, wantProto: 1},
	} {
		for _, method := range []string{http.MethodHead, http.MethodGet} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				const samples = 10
				type observation struct {
					proto  int
					method string
					uri    string
					peer   string
				}
				requests := make(chan observation, samples)
				closed := make(chan struct{}, samples)
				var accepts atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- observation{r.ProtoMajor, r.Method, r.RequestURI, r.RemoteAddr}
					w.WriteHeader(http.StatusNoContent)
				}))
				server.EnableHTTP2 = tc.serverH2
				server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					switch state {
					case http.StateNew:
						accepts.Add(1)
					case http.StateClosed:
						closed <- struct{}{}
					}
				}
				server.StartTLS()
				defer server.Close()
				client := http2FixtureClient(t, context.Background(), server, 2*time.Second, tc.keepAlive, tc.http2)
				var firstPeer string
				for range samples {
					if _, err := client.MeasureDelay(method); err != nil {
						t.Fatal(err)
					}
					got := <-requests
					if got.proto != tc.wantProto || got.method != method || got.uri != "/generate_204?ob=hkt" {
						t.Fatalf("request=%+v, want HTTP/%d %s with unchanged URL", got, tc.wantProto, method)
					}
					if firstPeer == "" {
						firstPeer = got.peer
					} else if tc.keepAlive && got.peer != firstPeer {
						t.Fatalf("keepalive changed peer from %s to %s", firstPeer, got.peer)
					}
				}
				wantConnections := int32(samples)
				if tc.keepAlive {
					wantConnections = 1
				}
				if got := accepts.Load(); got != wantConnections {
					t.Fatalf("accepted connections=%d, want %d", got, wantConnections)
				}
				client.CloseIdleConnections()
				for range wantConnections {
					contractWait(t, closed, "probe connection closed")
				}
			})
		}
	}
}

func TestHTTP2ConcurrentProbeStreamsReuseConnection(t *testing.T) {
	contractLoopbackDialer(t)
	const streams = 8
	var accepts atomic.Int32
	var armed atomic.Bool
	entered := make(chan string, streams)
	release := make(chan struct{})
	closed := make(chan struct{}, streams+1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.RequestURI != "/generate_204?ob=hkt" {
			t.Errorf("unexpected multiplexed request: %s %s", r.Proto, r.RequestURI)
		}
		if armed.Load() {
			entered <- r.RemoteAddr
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			accepts.Add(1)
		case http.StateClosed:
			closed <- struct{}{}
		}
	}
	server.StartTLS()
	defer server.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client := http2FixtureClient(t, context.Background(), server, 2*time.Second, true, true)
	if _, err := client.MeasureDelay(http.MethodHead); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	results := make(chan error, streams)
	for range streams {
		go func() {
			_, err := client.MeasureDelay(http.MethodHead)
			results <- err
		}()
	}
	var peer string
	for range streams {
		select {
		case got := <-entered:
			if peer == "" {
				peer = got
			} else if peer != got {
				t.Fatalf("concurrent HTTP/2 stream used a different peer: %s != %s", got, peer)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent streams did not enter before releasing their responses")
		}
	}
	unblock()
	for range streams {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := accepts.Load(); got != 1 {
		t.Fatalf("accepted connections=%d, want 1 for concurrent HTTP/2 streams", got)
	}
	client.CloseIdleConnections()
	contractWait(t, closed, "multiplexed probe connection closed")
}

func TestHTTP2ProbeStatusAndTLSValidation(t *testing.T) {
	contractLoopbackDialer(t)
	var redirects atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("request protocol=%s, want HTTP/2", r.Proto)
		}
		switch r.URL.Path {
		case "/200":
			w.WriteHeader(http.StatusOK)
		case "/302":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/target":
			redirects.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := http2FixtureClient(t, context.Background(), server, time.Second, true, true)
	for _, status := range []int{204, 200, 302, 503} {
		client.destination = fmt.Sprintf("%s/%d", server.URL, status)
		delay, err := client.MeasureDelay(http.MethodHead)
		if (err == nil) != (status == 204) || (err != nil && delay != rttFailed) {
			t.Fatalf("HTTP %d: delay=%v error=%v", status, delay, err)
		}
	}
	if redirects.Load() != 0 {
		t.Fatal("HTTP/2 probe followed a redirect")
	}
	untrusted := newPingClient(context.Background(), nil, server.URL, time.Second, "probe", true, true)
	defer untrusted.CloseIdleConnections()
	if delay, err := untrusted.MeasureDelay(http.MethodHead); err == nil || delay != rttFailed {
		t.Fatalf("untrusted TLS accepted: delay=%v error=%v", delay, err)
	}
}

func TestHTTP2ProbeTimeoutAndCancellation(t *testing.T) {
	contractLoopbackDialer(t)
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_parent=%v", cancelParent), func(t *testing.T) {
			entered := make(chan struct{})
			exited := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Errorf("request protocol=%s, want HTTP/2", r.Proto)
				}
				close(entered)
				<-r.Context().Done()
				close(exited)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 200 * time.Millisecond
			if cancelParent {
				timeout = 2 * time.Second
			}
			client := http2FixtureClient(t, ctx, server, timeout, true, true)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if delay, err := client.MeasureDelay(http.MethodHead); err == nil || delay != rttFailed {
					t.Errorf("blocked probe: delay=%v error=%v", delay, err)
				}
			}()
			contractWait(t, entered, "HTTP/2 handler entered")
			if cancelParent {
				cancel()
			}
			contractWait(t, done, "HTTP/2 probe exited")
			contractWait(t, exited, "HTTP/2 stream canceled")
			client.CloseIdleConnections()
		})
	}
}

func TestHTTP2OptionDoesNotEnableH2C(t *testing.T) {
	contractLoopbackDialer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("plaintext probe protocol=%s, want HTTP/1.1", r.Proto)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newPingClient(context.Background(), nil, server.URL, time.Second, "probe", true, true)
	defer client.CloseIdleConnections()
	if _, err := client.MeasureDelay(http.MethodHead); err != nil {
		t.Fatal(err)
	}
}
