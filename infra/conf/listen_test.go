package conf_test

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/infra/conf"
)

func TestInboundListenCompatibility(t *testing.T) {
	for _, value := range []string{`"127.0.0.1"`, `"0.0.0.0"`, `"::"`, `"localhost"`, `"/tmp/xray.sock"`, `null`, `["127.0.0.1","::1"]`} {
		t.Run(value, func(t *testing.T) {
			input := `{"protocol":"dokodemo-door","port":12345,"settings":{"address":"127.0.0.1","port":80},"listen":` + value + `}`
			var c conf.InboundDetourConfig
			if err := json.Unmarshal([]byte(input), &c); err != nil {
				t.Fatal(err)
			}
			built, err := c.Build()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := built.ReceiverSettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			receiver := raw.(*proxyman.ReceiverConfig)
			if value[0] == '[' {
				if receiver.Listen != nil || len(receiver.ListenAddresses) != 2 {
					t.Fatalf("unexpected receiver: %v", receiver)
				}
			} else if len(receiver.ListenAddresses) != 0 {
				t.Fatal("legacy config used new field")
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip conf.InboundDetourConfig
			if err := json.Unmarshal(data, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if len(roundtrip.ListenAddresses) != len(c.ListenAddresses) {
				t.Fatal("listen array lost during marshal")
			}
		})
	}
	var omitted conf.InboundDetourConfig
	if err := json.Unmarshal([]byte(`{"protocol":"dokodemo-door","port":12345,"settings":{"address":"127.0.0.1","port":80}}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if _, err := omitted.Build(); err != nil {
		t.Fatal(err)
	}
}

func TestInboundListenRejectInvalidArrays(t *testing.T) {
	for _, value := range []string{`[]`, `[null]`, `["localhost"]`, `["/tmp/xray.sock"]`, `["0.0.0.0"]`, `["::"]`, `["127.0.0.1","127.0.0.1"]`, `["::1","0:0:0:0:0:0:0:1"]`, `[123]`, `"127.0.0.1,127.0.0.2"`} {
		t.Run(value, func(t *testing.T) {
			var c conf.InboundDetourConfig
			err := json.Unmarshal([]byte(`{"protocol":"dokodemo-door","port":12345,"listen":`+value+`}`), &c)
			if err == nil {
				_, err = c.Build()
			}
			if err == nil {
				t.Fatal("accepted invalid listen")
			}
		})
	}
}

func TestInboundListenArrayRequiresPortAndSupportedProtocol(t *testing.T) {
	for _, input := range []string{
		`{"protocol":"dokodemo-door","listen":["127.0.0.1"]}`,
		`{"protocol":"tun","port":12345,"listen":["127.0.0.1"]}`,
	} {
		var c conf.InboundDetourConfig
		if err := json.Unmarshal([]byte(input), &c); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Build(); err == nil {
			t.Fatal("accepted unsupported listen array")
		}
	}
}
