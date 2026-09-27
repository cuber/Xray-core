package anytls

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common/protocol"
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
	if len(strings.Join(c.PaddingScheme, "\n")) > 8192 {
		return fmt.Errorf("anytls: padding scheme too large")
	}
	seen, stop := map[string]bool{}, false
	for _, line := range c.PaddingScheme {
		key, value, ok := strings.Cut(line, "=")
		if !ok || seen[key] {
			return fmt.Errorf("anytls: invalid padding scheme")
		}
		seen[key] = true
		if key == "stop" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || n > 256 {
				return fmt.Errorf("anytls: invalid padding stop")
			}
			stop = true
			continue
		}
		n, e := strconv.Atoi(key)
		if e != nil || n < 0 || n > 255 || strconv.Itoa(n) != key {
			return fmt.Errorf("anytls: invalid padding index")
		}
		fields := strings.Split(value, ",")
		if len(fields) > 16 {
			return fmt.Errorf("anytls: too many padding ranges")
		}
		for _, field := range fields {
			if field == "c" {
				continue
			}
			lo, hi, ok := strings.Cut(field, "-")
			l, le := strconv.Atoi(lo)
			h, he := strconv.Atoi(hi)
			if !ok || le != nil || he != nil || l < 1 || h < l || h > 65535 {
				return fmt.Errorf("anytls: invalid padding range")
			}
		}
	}
	if !stop {
		return fmt.Errorf("anytls: padding stop is required")
	}
	return nil
}
