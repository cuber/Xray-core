package hysteria

import (
	"context"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common/errors"
)

// Relay sessions (spec024 R2): an owner's connection carries other users of the same server inbound. The
// relay list is negotiated in the authentication request, each relayed stream names its user by index.
const (
	// FrameTypeTCPRelayRequest starts a relayed TCP stream: the varint index, then a FrameTypeTCPRequest body.
	FrameTypeTCPRelayRequest = 0x402

	// HeaderRelay carries the relay list in the authentication request and the answers in the response.
	HeaderRelay = "Hysteria-Relay"

	RelayStatusOK      = "ok"
	RelayStatusUnknown = "unknown"
	RelayStatusDenied  = "denied"

	// MaxRelayIndex bounds the indexes; 0 is the connection user.
	MaxRelayIndex = 255

	// A relayed UDP session ID carries the index in its top 8 bits.
	relayIndexShift = 24
	relaySeqMask    = 1<<relayIndexShift - 1
)

// RelayBinding is the server's answer for one index, as the client reads it.
type RelayBinding struct {
	Status string
	// The relayed user's email; set for RelayStatusOK.
	User string
}

type relayIndexKey struct{}

// ContextWithRelayIndex makes Dial open the stream or UDP session for the relay outbound with this index.
func ContextWithRelayIndex(ctx context.Context, index uint32) context.Context {
	return context.WithValue(ctx, relayIndexKey{}, index)
}

// RelayIndexFromContext returns the relay index of a dial, 0 for the connection user.
func RelayIndexFromContext(ctx context.Context) uint32 {
	index, _ := ctx.Value(relayIndexKey{}).(uint32)
	return index
}

func relayEncode(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func relayDecode(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err
}

// encodeRelayRequest lists the credentials as index=base64url, the index being the position + 1.
func encodeRelayRequest(auths []string) string {
	parts := make([]string, len(auths))
	for i, auth := range auths {
		parts[i] = strconv.Itoa(i+1) + "=" + relayEncode(auth)
	}
	return strings.Join(parts, ",")
}

func splitRelayEntries(v string, each func(index uint32, value string) error) error {
	if v == "" {
		return nil
	}
	seen := make(map[uint32]bool)
	for _, entry := range strings.Split(v, ",") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return errors.New("relay entry without '=': ", entry)
		}
		index, err := strconv.ParseUint(key, 10, 32)
		if err != nil || index < 1 || index > MaxRelayIndex {
			return errors.New("relay index out of 1–", MaxRelayIndex, ": ", key)
		}
		if seen[uint32(index)] {
			return errors.New("relay index twice: ", key)
		}
		seen[uint32(index)] = true
		if err := each(uint32(index), value); err != nil {
			return err
		}
	}
	return nil
}

// decodeRelayRequest returns the credentials by index.
func decodeRelayRequest(v string) (map[uint32]string, error) {
	auths := make(map[uint32]string)
	err := splitRelayEntries(v, func(index uint32, value string) error {
		auth, err := relayDecode(value)
		if err != nil {
			return errors.New("relay credential is not base64url").Base(err)
		}
		auths[index] = auth
		return nil
	})
	return auths, err
}

// encodeRelayResponse answers every index in order: ok carries the user's email.
func encodeRelayResponse(answers map[uint32]RelayBinding) string {
	indexes := make([]int, 0, len(answers))
	for index := range answers {
		indexes = append(indexes, int(index))
	}
	sort.Ints(indexes)
	parts := make([]string, len(indexes))
	for i, index := range indexes {
		answer := answers[uint32(index)]
		value := answer.Status
		if answer.Status == RelayStatusOK {
			value += ":" + relayEncode(answer.User)
		}
		parts[i] = strconv.Itoa(index) + "=" + value
	}
	return strings.Join(parts, ",")
}

// decodeRelayResponse reads the server's answers by index.
func decodeRelayResponse(v string) (map[uint32]RelayBinding, error) {
	answers := make(map[uint32]RelayBinding)
	err := splitRelayEntries(v, func(index uint32, value string) error {
		status, user, _ := strings.Cut(value, ":")
		switch status {
		case RelayStatusOK:
			email, err := relayDecode(user)
			if err != nil {
				return errors.New("relay user is not base64url").Base(err)
			}
			answers[index] = RelayBinding{Status: status, User: email}
		case RelayStatusUnknown, RelayStatusDenied:
			answers[index] = RelayBinding{Status: status}
		default:
			return errors.New("unknown relay status: ", status)
		}
		return nil
	})
	return answers, err
}

// relaySessionID is a relayed UDP session's ID: the index in the top 8 bits, the sequence in the low 24.
func relaySessionID(index, seq uint32) uint32 {
	return index<<relayIndexShift | seq&relaySeqMask
}

// relayIndexOf is the index a UDP session ID carries on a relay connection.
func relayIndexOf(id uint32) uint32 {
	return id >> relayIndexShift
}
