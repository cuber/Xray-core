package anytls

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/net"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport/internet"
)

// Validate checks native control-plane configs as well as JSON-built configs.
func (c *ClientConfig) Validate() error {
	if c == nil || c.Server == nil || c.Server.Address == nil {
		return fmt.Errorf("anytls: server address is required")
	}
	switch address := c.Server.Address.Address.(type) {
	case *net.IPOrDomain_Ip:
		if address == nil || (len(address.Ip) != 4 && len(address.Ip) != 16) {
			return fmt.Errorf("anytls: invalid server IP")
		}
	case *net.IPOrDomain_Domain:
		if address == nil || strings.TrimSpace(address.Domain) == "" || strings.TrimSpace(address.Domain) != address.Domain {
			return fmt.Errorf("anytls: invalid server domain")
		}
	default:
		return fmt.Errorf("anytls: server address is required")
	}
	if c.Server.Port == 0 || c.Server.Port > 65535 {
		return fmt.Errorf("anytls: invalid server port")
	}
	if c.Server.User == nil || c.Server.User.Account == nil {
		return fmt.Errorf("anytls: server account is required")
	}
	user, err := c.Server.User.ToMemoryUser()
	if err != nil {
		return fmt.Errorf("anytls: invalid server account")
	}
	if _, ok := user.Account.(*MemoryAccount); !ok {
		return fmt.Errorf("anytls: wrong server account type")
	}
	if c.MinIdleSession > math.MaxInt32 {
		return fmt.Errorf("anytls: minIdleSession exceeds portable integer range")
	}
	if c.MaxSessions > 4096 || c.MaxIdleSessions > 4096 || c.MaxConcurrentDials > 4096 {
		return fmt.Errorf("anytls: pool limit too large")
	}
	limits := engine.ClientOptions{MinIdleSession: int(c.MinIdleSession), MaxSessions: int(c.MaxSessions), MaxIdleSessions: int(c.MaxIdleSessions), MaxConcurrentDials: int(c.MaxConcurrentDials)}
	return limits.ValidateLimits()
}

// ValidateSender is shared by JSON loading and native HandlerService creation.
func (*ClientConfig) ValidateSender(sender *proxyman.SenderConfig) error {
	if sender == nil {
		return fmt.Errorf("anytls requires RAW TCP and TLS")
	}
	m, err := internet.ToMemoryStreamConfig(sender.StreamSettings)
	if err != nil {
		return err
	}
	if m == nil || m.ProtocolName != "tcp" || m.SecurityType != "xray.transport.internet.tls.Config" {
		return fmt.Errorf("anytls requires RAW TCP and TLS")
	}
	if sender.MultiplexSettings.GetEnabled() {
		return fmt.Errorf("anytls uses native multiplexing; outer mux must be disabled")
	}
	if domain := sender.Via.GetDomain(); domain == "origin" || domain == "srcip" {
		return fmt.Errorf("anytls pooled sessions require a fixed sendThrough address")
	}
	return nil
}

// Match the reference engine: values up to five seconds select its 30s default.
func clientIdleDuration(seconds uint32) time.Duration {
	if seconds <= 5 {
		return 30 * time.Second
	}
	return time.Duration(seconds) * time.Second
}
