package conf

import (
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/anytls"
	"google.golang.org/protobuf/proto"
)

type AnyTLSUserConfig struct {
	Password string `json:"password"`
	Email    string `json:"email"`
	Level    uint32 `json:"level"`
}
type AnyTLSServerConfig struct {
	Clients              []AnyTLSUserConfig `json:"clients"`
	PaddingScheme        []string           `json:"paddingScheme"`
	MaxSessions          uint32             `json:"maxSessions"`
	MaxSessionsPerUser   uint32             `json:"maxSessionsPerUser"`
	MaxStreamsPerSession uint32             `json:"maxStreamsPerSession"`
}

func (c *AnyTLSServerConfig) Build() (proto.Message, error) {
	config := &anytls.ServerConfig{PaddingScheme: c.PaddingScheme, MaxSessions: c.MaxSessions, MaxSessionsPerUser: c.MaxSessionsPerUser, MaxStreamsPerSession: c.MaxStreamsPerSession}
	for _, u := range c.Clients {
		config.Users = append(config.Users, &protocol.User{Email: u.Email, Level: u.Level, Account: serial.ToTypedMessage(&anytls.Account{Password: u.Password})})
	}
	return config, config.Validate()
}
