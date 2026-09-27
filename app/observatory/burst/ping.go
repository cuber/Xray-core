package burst

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/utils"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/tagged"
)

type pingClient struct {
	ctx         context.Context
	destination string
	httpClient  *http.Client
}

type pingConnection struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *pingConnection) Close() error {
	c.cancel()
	return c.Conn.Close()
}

func newPingClient(ctx context.Context, dispatcher routing.Dispatcher, destination string, timeout time.Duration, handler string, keepAlive bool) *pingClient {
	return &pingClient{
		ctx:         ctx,
		destination: destination,
		httpClient:  newHTTPClient(ctx, dispatcher, handler, timeout, keepAlive),
	}
}

func newDirectPingClient(destination string, timeout time.Duration) *pingClient {
	return &pingClient{
		destination: destination,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func newHTTPClient(ctxv context.Context, dispatcher routing.Dispatcher, handler string, timeout time.Duration, keepAlive bool) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives: !keepAlive,
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
	req, err := http.NewRequestWithContext(ctx, httpMethod, s.destination, nil)
	if err != nil {
		return rttFailed, err
	}
	utils.TryDefaultHeadersWith(req.Header, "nav")

	start := time.Now()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return rttFailed, err
	}
	if httpMethod == http.MethodGet {
		_, err = io.Copy(io.Discard, resp.Body)
		if err != nil {
			resp.Body.Close()
			return rttFailed, err
		}
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return rttFailed, fmt.Errorf("unexpected health check HTTP status: %s", resp.Status)
	}

	return time.Since(start), nil
}
