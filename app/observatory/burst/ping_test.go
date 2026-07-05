package burst

import (
	"context"
	"net/http"
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
