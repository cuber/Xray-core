package conf_test

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/xtls/xray-core/infra/conf"
)

func TestDNSOutboundTags(t *testing.T) {
	var parsed DNSConfig
	if err := json.Unmarshal([]byte(`{"servers":[{"address":"1.1.1.1","outboundTags":["exit-a"," exit-b"]},"8.8.8.8"]}`), &parsed); err != nil {
		t.Fatal(err)
	}
	built, err := parsed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got := built.NameServer[0].OutboundTag; len(got) != 2 || got[0] != "exit-a" || got[1] != "exit-b" {
		t.Fatalf("outbound tags = %q, want trimmed exit-a, exit-b", got)
	}
	if got := built.NameServer[1].OutboundTag; got != nil {
		t.Fatalf("unrestricted server tags = %q, want none", got)
	}
	if built.NameServer[0].PolicyID == built.NameServer[1].PolicyID {
		t.Fatal("servers differing by outboundTags share a parallel-query policy")
	}

	for _, test := range []struct {
		name, input, wantError string
	}{
		{name: "empty list", input: `{"servers":[{"address":"1.1.1.1","outboundTags":[]}]}`, wantError: "at least one"},
		{name: "empty tag", input: `{"servers":[{"address":"1.1.1.1","outboundTags":[" "]}]}`, wantError: "empty tag"},
		{name: "duplicate", input: `{"servers":[{"address":"1.1.1.1","outboundTags":["a","a"]}]}`, wantError: "twice"},
		{name: "script", input: `{"script":"s.lua","servers":[{"address":"1.1.1.1","outboundTags":["a"]}]}`, wantError: "outboundTags"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var config DNSConfig
			if err := json.Unmarshal([]byte(test.input), &config); err != nil {
				t.Fatal(err)
			}
			if built, err := config.Build(); built != nil || err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Build() = %v, %v; want error mentioning %q", built, err, test.wantError)
			}
		})
	}
}
