package quicrecovery

import (
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/transport/internet"
)

// spec021 FR-005: the R1 and R2 fields reach quic.Config, and unset ones keep quic-go's values.
func TestApply(t *testing.T) {
	var unset quic.Config
	Apply(&unset, nil)
	Apply(&unset, &internet.QuicParams{})
	if unset.LossRecovery() != (&quic.Config{}).LossRecovery() || unset.MaxAckDelay != 0 || unset.DuplicateMaxSize != 0 {
		t.Fatalf("unset fields must leave the config as it is: %+v", unset.LossRecovery())
	}

	var c quic.Config
	Apply(&c, &internet.QuicParams{
		MaxAckDelayMs: 2, AckElicitingThreshold: 1, InitialRttMs: 15, PtoBackoffLimit: 1,
		LossTimeThreshold: 1.25, LossPacketThreshold: 4, DuplicateMaxSize: 300, DuplicateTail: true, DuplicateDelayMs: 3,
	})
	want := quic.LossRecovery{
		MaxAckDelay: 2 * time.Millisecond, AckElicitingThreshold: 1, InitialRTT: 15 * time.Millisecond, PTOBackoffLimit: 1,
		LossTimeThreshold: 1.25, LossPacketThreshold: 4, DuplicateMaxSize: 300, DuplicateTail: true, DuplicateDelay: 3 * time.Millisecond,
	}
	if got := c.LossRecovery(); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestEffectiveDefaults(t *testing.T) {
	got := Effective(nil)
	want := quic.LossRecovery{
		MaxAckDelay: 25 * time.Millisecond, AckElicitingThreshold: 2, InitialRTT: 100 * time.Millisecond,
		LossTimeThreshold: 9.0 / 8, LossPacketThreshold: 3,
	}
	if got != want {
		t.Fatalf("got %+v, want quic-go's defaults %+v", got, want)
	}
	if Effective(&internet.QuicParams{InitialRttMs: 20}).InitialRTT != 20*time.Millisecond {
		t.Fatal("a set field is effective")
	}
}
