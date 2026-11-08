package hysteria

import (
	"context"
	"strings"
	"testing"
)

// Core spec024 FR-003: the relay negotiation's encoding and the UDP ID layout.

func TestRelayRequestRoundTrip(t *testing.T) {
	auths := []string{"user:a:1", "plain secret", "with,comma=and=equals", ""}
	encoded := encodeRelayRequest(auths)
	if strings.Count(encoded, ",") != len(auths)-1 || strings.ContainsAny(encoded, " :") {
		t.Fatalf("credentials must not leak separators: %q", encoded)
	}
	decoded, err := decodeRelayRequest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for i, auth := range auths {
		if decoded[uint32(i+1)] != auth {
			t.Errorf("index %d: %q, want %q", i+1, decoded[uint32(i+1)], auth)
		}
	}
	if got, err := decodeRelayRequest(""); err != nil || len(got) != 0 {
		t.Errorf("empty request: %v, %v", got, err)
	}
}

func TestRelayResponseRoundTrip(t *testing.T) {
	answers := map[uint32]RelayBinding{
		3: {Status: RelayStatusDenied},
		1: {Status: RelayStatusOK, User: "a@relay.test"},
		2: {Status: RelayStatusUnknown},
	}
	encoded := encodeRelayResponse(answers)
	if !strings.HasPrefix(encoded, "1=ok:") || !strings.HasSuffix(encoded, ",2=unknown,3=denied") {
		t.Fatalf("answers in index order: %q", encoded)
	}
	decoded, err := decodeRelayResponse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range answers {
		if decoded[index] != want {
			t.Errorf("index %d: %+v, want %+v", index, decoded[index], want)
		}
	}
}

func TestRelayMalformedEntriesAreRefused(t *testing.T) {
	for _, v := range []string{"1", "0=YQ", "256=YQ", "x=YQ", "1=YQ,1=Yg", "1=@@"} {
		if _, err := decodeRelayRequest(v); err == nil {
			t.Errorf("request %q accepted", v)
		}
	}
	for _, v := range []string{"1=maybe", "1=ok:@@", "2", "1=ok,1=denied"} {
		if _, err := decodeRelayResponse(v); err == nil {
			t.Errorf("response %q accepted", v)
		}
	}
}

func TestRelaySessionIDCarriesTheIndex(t *testing.T) {
	for _, tc := range []struct{ index, seq uint32 }{{1, 0}, {1, 1}, {255, relaySeqMask}, {7, relaySeqMask + 5}} {
		id := relaySessionID(tc.index, tc.seq)
		if relayIndexOf(id) != tc.index || id&relaySeqMask != tc.seq&relaySeqMask {
			t.Errorf("index %d seq %d: id %#x", tc.index, tc.seq, id)
		}
	}
	if relayIndexOf(41) != 0 {
		t.Error("an ID below 2^24 is the connection user's")
	}
}

func TestRelayIndexContext(t *testing.T) {
	ctx := context.Background()
	if RelayIndexFromContext(ctx) != 0 {
		t.Error("no index is the connection user")
	}
	if RelayIndexFromContext(ContextWithRelayIndex(ctx, 9)) != 9 {
		t.Error("index lost")
	}
}
