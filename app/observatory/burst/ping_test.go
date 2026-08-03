package burst

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHTTPClientKeepAlive(t *testing.T) {
	cases := []struct {
		name                  string
		keepAlive             bool
		wantDisableKeepAlives bool
	}{
		{
			name:                  "legacy short connections",
			keepAlive:             false,
			wantDisableKeepAlives: true,
		},
		{
			name:                  "keep alive enabled",
			keepAlive:             true,
			wantDisableKeepAlives: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newHTTPClient(context.Background(), nil, "test-handler", time.Second, tc.keepAlive)
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
			}
			if transport.DisableKeepAlives != tc.wantDisableKeepAlives {
				t.Fatalf("DisableKeepAlives = %v, want %v", transport.DisableKeepAlives, tc.wantDisableKeepAlives)
			}
		})
	}
}

func TestMeasureDelayRequiresNoContent(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "204 is healthy", statusCode: http.StatusNoContent},
		{name: "200 is not a generate-204 response", statusCode: http.StatusOK, wantErr: true},
		{name: "503 is unhealthy", statusCode: http.StatusServiceUnavailable, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
			}))
			defer server.Close()

			client := &pingClient{destination: server.URL, httpClient: server.Client()}
			_, err := client.MeasureDelay(http.MethodHead)
			if (err != nil) != test.wantErr {
				t.Fatalf("MeasureDelay() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestHealthPingAliveFollowsLatestResult(t *testing.T) {
	result := NewHealthPingResult(4, time.Hour)
	result.Put(rttFailed)
	if stats := result.Get(); stats.Alive || stats.All != 1 || stats.Fail != 1 {
		t.Fatalf("first failed sample stats = %+v, want dead with 1/1 failures", stats)
	}

	result.Put(10 * time.Millisecond)
	if stats := result.Get(); !stats.Alive {
		t.Fatalf("recovery sample stats = %+v, want alive", stats)
	}

	result.Put(rttFailed)
	if stats := result.Get(); stats.Alive || stats.Fail == stats.All {
		t.Fatalf("latest failed sample stats = %+v, want dead before the whole window fails", stats)
	}
}
