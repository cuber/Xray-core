package anytls

import (
	"math"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/proto"
)

func validClientConfig() *ClientConfig {
	return &ClientConfig{Server: &protocol.ServerEndpoint{
		Address: net.NewIPOrDomain(net.DomainAddress("anytls.test")), Port: 443,
		User: &protocol.User{Account: serial.ToTypedMessage(&Account{Password: "secret"})},
	}}
}

func TestClientConfigValidation(t *testing.T) {
	for name, mutate := range map[string]func(*ClientConfig){
		"missing server":    func(c *ClientConfig) { c.Server = nil },
		"missing address":   func(c *ClientConfig) { c.Server.Address = nil },
		"unset address":     func(c *ClientConfig) { c.Server.Address = &net.IPOrDomain{} },
		"invalid IP":        func(c *ClientConfig) { c.Server.Address = &net.IPOrDomain{Address: &net.IPOrDomain_Ip{Ip: []byte{1}}} },
		"empty domain":      func(c *ClientConfig) { c.Server.Address = net.NewIPOrDomain(net.DomainAddress("")) },
		"zero port":         func(c *ClientConfig) { c.Server.Port = 0 },
		"overflow port":     func(c *ClientConfig) { c.Server.Port = 65536 },
		"missing user":      func(c *ClientConfig) { c.Server.User = nil },
		"missing account":   func(c *ClientConfig) { c.Server.User.Account = nil },
		"empty password":    func(c *ClientConfig) { c.Server.User.Account = serial.ToTypedMessage(&Account{}) },
		"wrong account":     func(c *ClientConfig) { c.Server.User.Account = serial.ToTypedMessage(&ServerConfig{}) },
		"idle overflow":     func(c *ClientConfig) { c.MinIdleSession = math.MaxUint32 },
		"session overflow":  func(c *ClientConfig) { c.MaxSessions = 4097 },
		"idle above total":  func(c *ClientConfig) { c.MaxSessions = 2; c.MaxIdleSessions = 3 },
		"dial above total":  func(c *ClientConfig) { c.MaxSessions = 2; c.MaxConcurrentDials = 3 },
		"minimum above cap": func(c *ClientConfig) { c.MaxIdleSessions = 1; c.MinIdleSession = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			c := validClientConfig()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	var absent *ClientConfig
	if absent.Validate() == nil {
		t.Fatal("nil config accepted")
	}
	c := validClientConfig()
	c.MaxSessions, c.MaxIdleSessions, c.MaxConcurrentDials = 32, 8, 4
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := proto.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var restored ClientConfig
	if err := proto.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(c, &restored) {
		t.Fatal("protobuf round trip changed config")
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClientIdleDuration(t *testing.T) {
	for _, seconds := range []uint32{0, 1, 5, 6, 30, math.MaxUint32} {
		want := time.Duration(seconds) * time.Second
		if seconds <= 5 {
			want = 30 * time.Second
		}
		if got := clientIdleDuration(seconds); got != want || got <= 0 {
			t.Fatalf("%d seconds: got %v want %v", seconds, got, want)
		}
	}
}
