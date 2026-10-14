package burst

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/retry"
	"github.com/xtls/xray-core/common/utils"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

type pingClient struct {
	ctx         context.Context
	destination string
	httpClient  *http.Client
	retryPolicy retry.ProbePolicy
}

type pingConnection struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *pingConnection) Close() error {
	c.cancel()
	return c.Conn.Close()
}

func newPingClient(ctx context.Context, dispatcher routing.Dispatcher, destination string, timeout time.Duration, handler string, keepAlive, http2 bool) *pingClient {
	return &pingClient{
		ctx:         ctx,
		destination: destination,
		httpClient:  newHTTPClient(ctx, dispatcher, handler, timeout, keepAlive, http2),
	}
}

func newDirectPingClient(destination string, timeout time.Duration) *pingClient {
	return &pingClient{
		destination: destination,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func newHTTPClient(ctxv context.Context, dispatcher routing.Dispatcher, handler string, timeout time.Duration, keepAlive, http2 bool) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives: !keepAlive,
		ForceAttemptHTTP2: http2,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dest, err := net.ParseDestination(network + ":" + addr)
			if err != nil {
				return nil, err
			}
			// Dispatch returns a pipe before protocol dialing/handshaking finishes.
			// Closing only that pipe cannot cancel an admission still in progress.
			dialCtx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(ctxv, cancel)
			cleanup := func() { stop(); cancel() }
			conn, err := tagged.Dialer(dialCtx, dispatcher, dest, handler)
			if err != nil {
				cleanup()
				if conn != nil {
					conn.Close()
				}
				return nil, err
			}
			return &pingConnection{Conn: conn, cancel: cleanup}, nil
		},
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		// don't follow redirect
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s *pingClient) CloseIdleConnections() {
	if s == nil || s.httpClient == nil {
		return
	}
	s.httpClient.CloseIdleConnections()
}

// MeasureDelay returns the delay time of the request to dest
func (s *pingClient) MeasureDelay(httpMethod string) (time.Duration, error) {
	if s.httpClient == nil {
		panic("pingClient not initialized")
	}

	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// One deadline covers the full logical sample, including response reads
	// and waits. Keep the shared transport alive for other concurrent samples.
	if s.httpClient.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.httpClient.Timeout)
		defer cancel()
	}
	start := time.Now()
	result, err := s.retryPolicy.Do(ctx, func(attemptCtx context.Context) error {
		return s.measureOnce(attemptCtx, httpMethod)
	})
	if err != nil {
		if result.Attempts > 1 {
			return rttFailed, errors.New("health probe failed after ", result.Attempts, " attempts: ").Base(err)
		}
		return rttFailed, err
	}
	if result.Attempts > 1 {
		errors.LogInfo(ctx, "health probe recovered after ", result.Attempts-1, " retries; elapsed=", time.Since(start))
	}
	return time.Since(start), nil
}

func (s *pingClient) measureOnce(ctx context.Context, httpMethod string) error {
	req, err := http.NewRequestWithContext(ctx, httpMethod, s.destination, nil)
	if err != nil {
		return err
	}
	utils.TryDefaultHeadersWith(req.Header, "nav")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		resp.Body.Close()
		return fmt.Errorf("unexpected health check HTTP status: %s", resp.Status)
	}
	if httpMethod == http.MethodGet {
		_, err = io.Copy(io.Discard, resp.Body)
		if err != nil {
			resp.Body.Close()
			return err
		}
	}
	resp.Body.Close()
	return nil
}
