package burst

import "testing"

func TestHealthPingSettings_destinationFor(t *testing.T) {
	const globalDest = "http://cp.cloudflare.com/generate_204"
	const sjwDest = "http://127.0.0.1:8080/generate_204"
	const busDest = "http://probe.example.com/204"

	cases := []struct {
		name   string
		byPref map[string]string
		tag    string
		want   string
	}{
		{
			name: "empty map returns global destination",
			tag:  "sjw-ss",
			want: globalDest,
		},
		{
			name:   "prefix match wins over global",
			byPref: map[string]string{"sjw-": sjwDest},
			tag:    "sjw-hy2",
			want:   sjwDest,
		},
		{
			name:   "no prefix match falls back to global",
			byPref: map[string]string{"sjw-": sjwDest},
			tag:    "bus-att-scc",
			want:   globalDest,
		},
		{
			name:   "longest prefix wins on overlap",
			byPref: map[string]string{"sjw-": sjwDest, "sjw-hy": busDest},
			tag:    "sjw-hy2",
			want:   busDest,
		},
		{
			name:   "empty value in map is ignored",
			byPref: map[string]string{"sjw-": ""},
			tag:    "sjw-ss",
			want:   globalDest,
		},
		{
			name:   "non-matching longer prefix is ignored",
			byPref: map[string]string{"sjw-foo-": sjwDest},
			tag:    "sjw-bar",
			want:   globalDest,
		},
		{
			name:   "empty-string key acts as catch-all fallback",
			byPref: map[string]string{"": busDest},
			tag:    "any-tag",
			want:   busDest,
		},
		{
			name:   "empty-string key loses to any matching non-empty prefix",
			byPref: map[string]string{"": busDest, "sjw-": sjwDest},
			tag:    "sjw-ss",
			want:   sjwDest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &HealthPingSettings{
				Destination:          globalDest,
				DestinationsByPrefix: tc.byPref,
			}
			if got := s.destinationFor(tc.tag); got != tc.want {
				t.Fatalf("destinationFor(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}
