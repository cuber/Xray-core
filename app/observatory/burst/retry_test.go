package burst

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/retry"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

func TestRetryProbeHTTPProtocolsAndReuse(t *testing.T) {
	contractLoopbackDialer(t)
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			var hits, dials atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				want := 1
				if h2 {
					want = 2
				}
				if r.ProtoMajor != want || r.RequestURI != "/generate_204?ob=hkt" {
					t.Errorf("request=%s %s", r.Proto, r.RequestURI)
				}
				w.WriteHeader(204)
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := http2FixtureClient(t, context.Background(), server, time.Second, true, h2)
			client.retryPolicy = retry.ProbePolicy{MaxRetries: 2, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 10 * time.Millisecond}
			transport := client.httpClient.Transport.(*http.Transport)
			dial := transport.DialContext
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if dials.Add(1) == 1 {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				return dial(ctx, network, addr)
			}
			delay, err := client.MeasureDelay(http.MethodHead)
			if err != nil || delay < 8*time.Millisecond {
				t.Fatalf("delay=%v err=%v", delay, err)
			}
			for i := 0; i < 5; i++ {
				if _, err := client.MeasureDelay(http.MethodHead); err != nil {
					t.Fatal(err)
				}
			}
			if dials.Load() != 2 || hits.Load() != 6 {
				t.Fatalf("dials=%d hits=%d", dials.Load(), hits.Load())
			}
		})
	}
}

type retryProbeTransport func(*http.Request) (*http.Response, error)

func (f retryProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type retryProbePoolTransport struct {
	retryProbeTransport
	pool *http.Transport
}

func (t retryProbePoolTransport) CloseIdleConnections() { t.pool.CloseIdleConnections() }

func TestRetryProbeHTTP2ConcurrentPoolIsolation(t *testing.T) {
	contractLoopbackDialer(t)
	const samples = 8
	var accepts atomic.Int32
	var armed atomic.Bool
	entered := make(chan string, samples)
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol=%s", r.Proto)
		}
		if armed.Load() {
			entered <- r.RemoteAddr
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(204)
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			accepts.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	client := http2FixtureClient(t, context.Background(), server, time.Second, true, true)
	if _, err := client.MeasureDelay(http.MethodHead); err != nil {
		t.Fatal(err)
	}
	client.retryPolicy = retry.ProbePolicy{MaxRetries: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	transport := client.httpClient.Transport.(*http.Transport)
	var injected atomic.Bool
	client.httpClient.Transport = retryProbePoolTransport{pool: transport, retryProbeTransport: func(r *http.Request) (*http.Response, error) {
		if !injected.Swap(true) {
			return nil, io.EOF
		}
		return transport.RoundTrip(r)
	}}
	var workers sync.WaitGroup
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		workers.Wait()
		client.CloseIdleConnections()
	}()
	armed.Store(true)
	done := make(chan error, samples)
	for i := 0; i < samples; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := client.MeasureDelay(http.MethodHead); done <- err }()
	}
	var peer string
	for i := 0; i < samples; i++ {
		select {
		case p := <-entered:
			if peer == "" {
				peer = p
			} else if p != peer {
				t.Fatal("retry reset the shared connection")
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent probes blocked")
		}
	}
	close(release)
	for i := 0; i < samples; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if accepts.Load() != 1 {
		t.Fatalf("connections=%d", accepts.Load())
	}
}

func TestRetryProbeStopDuringBackoff(t *testing.T) {
	old := tagged.Dialer
	t.Cleanup(func() { tagged.Dialer = old })
	entered := make(chan struct{}, 10)
	tagged.Dialer = func(context.Context, routing.Dispatcher, xnet.Destination, string) (xnet.Conn, error) {
		entered <- struct{}{}
		return nil, syscall.ECONNREFUSED
	}
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{Destination: "http://test.invalid/generate_204", Interval: int64(time.Hour), Timeout: int64(2 * time.Second), SamplingCount: 1, Retry: &ProbeRetryConfig{MaxRetries: 2, InitialBackoff: int64(time.Second), MaxBackoff: int64(time.Second)}})
	h.StartScheduler(func() ([]string, error) { return []string{"test"}, nil })
	defer h.StopScheduler()
	contractWait(t, entered, "failed attempt")
	done := make(chan struct{})
	go func() { h.StopScheduler(); close(done) }()
	contractWait(t, done, "stop cancels backoff")
	if len(h.Results) != 0 {
		t.Fatal("canceled retry published a sample")
	}
}

func TestRetryProbeTerminalHTTPAndTLS(t *testing.T) {
	contractLoopbackDialer(t)
	for _, code := range []int{200, 302, 401, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(code) }))
			defer server.Close()
			client := newPingClient(context.Background(), nil, server.URL, time.Second, "test", false, false)
			defer client.CloseIdleConnections()
			client.retryPolicy = retry.ProbePolicy{MaxRetries: 2}
			if _, err := client.MeasureDelay(http.MethodHead); err == nil || hits.Load() != 1 {
				t.Fatalf("err=%v hits=%d", err, hits.Load())
			}
		})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted certificate reached handler") }))
	defer server.Close()
	client := newPingClient(context.Background(), nil, server.URL, time.Second, "test", false, true)
	defer client.CloseIdleConnections()
	client.retryPolicy = retry.ProbePolicy{MaxRetries: 2}
	tr := client.httpClient.Transport.(*http.Transport)
	dial := tr.DialContext
	var dials atomic.Int32
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, addr)
	}
	if _, err := client.MeasureDelay(http.MethodHead); err == nil || dials.Load() != 1 {
		t.Fatalf("err=%v dials=%d", err, dials.Load())
	}
}

func TestRetryProbeTerminalHTTPTruncatedBody(t *testing.T) {
	var attempts atomic.Int32
	client := &pingClient{
		destination: "http://test.invalid/generate_204",
		retryPolicy: retry.ProbePolicy{MaxRetries: 2},
		httpClient: &http.Client{Timeout: time.Second, Transport: retryProbeTransport(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Body: io.NopCloser(retryProbeBrokenBody{})}, nil
		})},
	}
	if _, err := client.MeasureDelay(http.MethodGet); err == nil || attempts.Load() != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts.Load())
	}
}

type retryProbeBrokenBody struct{}

func (retryProbeBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRetryProbeSingleHealthSampleAndRecovery(t *testing.T) {
	old := tagged.Dialer
	t.Cleanup(func() { tagged.Dialer = old })
	var mode, dials atomic.Int32
	tagged.Dialer = func(ctx context.Context, _ routing.Dispatcher, dest xnet.Destination, _ string) (xnet.Conn, error) {
		n := dials.Add(1)
		if mode.Load() == 0 || n%3 != 0 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", dest.NetAddr())
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	h := NewHealthPing(context.Background(), nil, &HealthPingConfig{Destination: server.URL, Timeout: int64(time.Second), SamplingCount: 10, Retry: &ProbeRetryConfig{MaxRetries: 2, InitialBackoff: int64(time.Millisecond), MaxBackoff: int64(time.Millisecond)}})
	h.Check([]string{"test"})
	if got := h.Results["test"].Get(); got.Alive || got.All != 1 || got.Fail != 1 || dials.Load() != 3 {
		t.Fatalf("failed sample=%+v dials=%d", got, dials.Load())
	}
	mode.Store(1)
	for i := 1; i <= 3; i++ {
		h.Check([]string{"test"})
		got := h.Results["test"].Get()
		if got.All != i+1 || got.Fail != 1 || got.Alive != (i == 3) {
			t.Fatalf("recovery %d: %+v", i, got)
		}
	}
	if dials.Load() != 12 {
		t.Fatalf("dials=%d, want four logical checks x three attempts", dials.Load())
	}
}

func TestRetryProbeTotalBudgetAndStop(t *testing.T) {
	contractLoopbackDialer(t)
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%v", stop), func(t *testing.T) {
			var hits atomic.Int32
			entered := make(chan struct{})
			left := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				close(entered)
				<-r.Context().Done()
				close(left)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := newPingClient(ctx, nil, server.URL, 120*time.Millisecond, "test", false, false)
			defer client.CloseIdleConnections()
			client.retryPolicy = retry.ProbePolicy{MaxRetries: 2, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 10 * time.Millisecond}
			tr := client.httpClient.Transport.(*http.Transport)
			dial := tr.DialContext
			var dials atomic.Int32
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if dials.Add(1) == 1 {
					return nil, syscall.ECONNREFUSED
				}
				return dial(ctx, network, addr)
			}
			done := make(chan error, 1)
			start := time.Now()
			go func() { _, err := client.MeasureDelay(http.MethodHead); done <- err }()
			contractWait(t, entered, "second attempt entered")
			if stop {
				cancel()
			}
			select {
			case err := <-done:
				want := context.DeadlineExceeded
				if stop {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("err=%v want=%v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("probe exceeded budget")
			}
			contractWait(t, left, "timed out request released")
			if hits.Load() != 1 || dials.Load() != 2 || time.Since(start) > time.Second {
				t.Fatalf("hits=%d dials=%d elapsed=%v", hits.Load(), dials.Load(), time.Since(start))
			}
		})
	}
}
