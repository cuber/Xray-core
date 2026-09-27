package anytls

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/xtls/xray-core/common/protocol"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"google.golang.org/protobuf/proto"
)

type MemoryAccount struct {
	password string
	hash     [32]byte
}

func (a *Account) AsAccount() (protocol.Account, error) {
	if a.Password == "" {
		return nil, fmt.Errorf("anytls: empty password")
	}
	return &MemoryAccount{password: a.Password, hash: sha256.Sum256([]byte(a.Password))}, nil
}
func (a *MemoryAccount) Equals(b protocol.Account) bool {
	other, ok := b.(*MemoryAccount)
	return ok && subtle.ConstantTimeCompare(a.hash[:], other.hash[:]) == 1
}
func (a *MemoryAccount) ToProto() proto.Message { return &Account{Password: a.password} }

// Validate is shared by JSON and control-plane creation.
func (c *ServerConfig) Validate() error {
	emails, passwords := map[string]bool{}, map[[32]byte]bool{}
	for _, u := range c.Users {
		if u == nil || u.Email == "" {
			return fmt.Errorf("anytls: email is required")
		}
		m, err := u.ToMemoryUser()
		if err != nil {
			return fmt.Errorf("anytls: invalid account")
		}
		a, ok := m.Account.(*MemoryAccount)
		if !ok {
			return fmt.Errorf("anytls: wrong account type")
		}
		if emails[u.Email] || passwords[a.hash] {
			return fmt.Errorf("anytls: duplicate email or password")
		}
		emails[u.Email], passwords[a.hash] = true, true
	}
	if c.MaxSessions > 4096 || c.MaxSessionsPerUser > 4096 || c.MaxStreamsPerSession > 1024 {
		return fmt.Errorf("anytls: resource limit too large")
	}
	if len(c.PaddingScheme) == 0 {
		return nil
	}
	return engine.ValidatePaddingScheme([]byte(strings.Join(c.PaddingScheme, "\n")))
}
