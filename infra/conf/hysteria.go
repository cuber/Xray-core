package conf

import (
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/hysteria/account"
	"google.golang.org/protobuf/proto"
)

type HysteriaClientConfig struct {
	Version int32                `json:"version"`
	Address *Address             `json:"address"`
	Port    uint16               `json:"port"`
	Relay   *HysteriaRelayConfig `json:"relay"`
}

// HysteriaRelayConfig is a relay outbound (spec024 R1): it sends through the session owner's connections as
// the user whose credential it carries. Config.Build links it to its owner and gives it its index.
type HysteriaRelayConfig struct {
	Session string `json:"session"`
	Auth    string `json:"auth"`
}

func (c *HysteriaClientConfig) Build() (proto.Message, error) {
	if c.Version != 2 {
		return nil, errors.New("version != 2")
	}

	if c.Relay != nil {
		if c.Address != nil || c.Port != 0 {
			return nil, errors.New("a hysteria relay outbound has no address or port: it sends through its session owner")
		}
		if c.Relay.Session == "" {
			return nil, errors.New("hysteria relay.session must name the session owner's outbound tag")
		}
		if c.Relay.Auth == "" {
			return nil, errors.New("hysteria relay.auth must be the relayed user's credential")
		}
		return &hysteria.ClientConfig{Relay: &hysteria.Relay{Session: c.Relay.Session, Auth: c.Relay.Auth}}, nil
	}

	config := &hysteria.ClientConfig{}
	config.Server = &protocol.ServerEndpoint{
		Address: c.Address.Build(),
		Port:    uint32(c.Port),
	}

	return config, nil
}

type HysteriaUserConfig struct {
	Auth  string   `json:"auth"`
	Level uint32   `json:"level"`
	Email string   `json:"email"`
	Relay []string `json:"relay"`
}

type HysteriaServerConfig struct {
	Version int32                 `json:"version"`
	Users   []*HysteriaUserConfig `json:"users"`
	Clients []*HysteriaUserConfig `json:"clients"`
}

func (c *HysteriaServerConfig) Build() (proto.Message, error) {
	if c.Version != 2 {
		return nil, errors.New("version != 2")
	}

	config := new(hysteria.ServerConfig)

	if c.Clients != nil {
		c.Users = c.Clients
	}
	if err := checkHysteriaRelayLists(c.Users); err != nil {
		return nil, err
	}
	if len(c.Users) > 0 {
		config.Users = make([]*protocol.User, len(c.Users))
		processUser := func(idx int) error {
			user := c.Users[idx]
			acc := &account.Account{
				Auth:  user.Auth,
				Relay: user.Relay,
			}
			config.Users[idx] = &protocol.User{
				Email:   user.Email,
				Level:   user.Level,
				Account: serial.ToTypedMessage(acc),
			}
			return nil
		}
		if err := task.ParallelForN(len(c.Users), processUser); err != nil {
			return nil, err
		}
	}

	return config, nil
}

// checkHysteriaRelayLists enforces spec024 R1 on a server's clients: a relay list names other clients of the
// same inbound by email, each once.
func checkHysteriaRelayLists(users []*HysteriaUserConfig) error {
	emails := make(map[string]bool, len(users))
	for _, user := range users {
		if user.Email != "" {
			emails[user.Email] = true
		}
	}
	for _, user := range users {
		if len(user.Relay) == 0 {
			continue
		}
		if user.Email == "" {
			return errors.New("a hysteria client with a relay list needs an email")
		}
		seen := make(map[string]bool, len(user.Relay))
		for _, email := range user.Relay {
			switch {
			case email == user.Email:
				return errors.New("hysteria client ", user.Email, " lists itself in its relay list")
			case !emails[email]:
				return errors.New("hysteria client ", user.Email, " relays for ", email, ", which is not a client of this inbound")
			case seen[email]:
				return errors.New("hysteria client ", user.Email, " lists ", email, " twice in its relay list")
			}
			seen[email] = true
		}
	}
	return nil
}
