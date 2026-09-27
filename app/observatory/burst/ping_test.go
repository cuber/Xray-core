package burst

import (
	"context"
	"fmt"
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

func TestHealthPingWarmupAcceptsFirstCleanSample(t *testing.T) {
	result := NewHealthPingResult(25, time.Hour)
	if stats := result.Get(); stats.Alive {
		t.Fatalf("empty result stats = %+v, want not alive", stats)
	}

	result.Put(10 * time.Millisecond)
	if stats := result.Get(); !stats.Alive || stats.All != 1 || stats.Fail != 0 {
		t.Fatalf("first successful sample stats = %+v, want alive warmup", stats)
	}
}

func TestHealthPingRecoveryRequiresConsecutiveSuccesses(t *testing.T) {
	result := NewHealthPingResult(4, time.Hour)
	result.Put(rttFailed)
	if stats := result.Get(); stats.Alive || stats.All != 1 || stats.Fail != 1 {
		t.Fatalf("first failed sample stats = %+v, want dead with 1/1 failures", stats)
	}

	for success := 1; success < 3; success++ {
		result.Put(10 * time.Millisecond)
		if stats := result.Get(); stats.Alive {
			t.Fatalf("recovery success %d stats = %+v, want dead", success, stats)
		}
	}
	result.Put(10 * time.Millisecond)
	if stats := result.Get(); !stats.Alive {
		t.Fatalf("third consecutive success stats = %+v, want alive", stats)
	}

	result.Put(rttFailed)
	if stats := result.Get(); stats.Alive || stats.Fail == stats.All {
		t.Fatalf("latest failed sample stats = %+v, want dead before the whole window fails", stats)
	}
}

func TestHealthPingRecoveryThresholdUsesWindowCapacity(t *testing.T) {
	for _, test := range []struct {
		capacity int
		want     int
	}{
		{capacity: 1, want: 1},
		{capacity: 2, want: 2},
		{capacity: 3, want: 3},
		{capacity: 25, want: 3},
	} {
		t.Run(fmt.Sprintf("capacity_%d", test.capacity), func(t *testing.T) {
			result := NewHealthPingResult(test.capacity, time.Hour)
			result.Put(rttFailed)
			for success := 1; success <= test.want; success++ {
				result.Put(10 * time.Millisecond)
				wantAlive := success == test.want
				if got := result.Get().Alive; got != wantAlive {
					t.Fatalf("success %d alive = %v, want %v", success, got, wantAlive)
				}
			}
		})
	}
}

func TestHealthPingFailureResetsRecoveryStreak(t *testing.T) {
	result := NewHealthPingResult(3, time.Hour)
	result.Put(rttFailed)
	result.Put(10 * time.Millisecond)
	result.Put(10 * time.Millisecond)
	result.Put(rttFailed)

	result.Put(10 * time.Millisecond)
	result.Put(10 * time.Millisecond)
	if stats := result.Get(); stats.Alive {
		t.Fatalf("two successes after reset stats = %+v, want dead", stats)
	}
	result.Put(10 * time.Millisecond)
	if stats := result.Get(); !stats.Alive {
		t.Fatalf("three successes after reset stats = %+v, want alive", stats)
	}
}
