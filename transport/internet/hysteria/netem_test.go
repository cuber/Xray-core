//go:build linux

package hysteria

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/transport/internet"
)

// spec021 FR-010 (user, 2026-11-07: "全量测试里是不是可以考虑用tc模拟一些丢包来验证"; "把这个测试做到测用例里，
// 必须要验证到且要符合预期"): the Hy2 loss tests on the kernel's netem instead of a wrapped socket. They run inside
// an isolated network namespace whose only interface is lo, where they own tc: testing/netem/run.sh. There
// XRAY_NETEM=1 turns every missing precondition into a failure, never a skip.

// netemRTT is the emulated round trip: netem delays each packet on lo once, so each direction pays half of it.
// It is about wg-ix's 7.5 ms (xray-config spec023).
const netemRTT = 7500 * time.Microsecond

func requireNetem(t *testing.T) {
	t.Helper()
	if os.Getenv("XRAY_NETEM") != "1" {
		t.Skip("netem tests run through testing/netem/run.sh")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Name != "lo" {
			t.Fatalf("refusing to change tc: interface %s exists, so this is not an isolated namespace", iface.Name)
		}
	}
	if _, err := exec.LookPath("tc"); err != nil {
		t.Fatal("tc is missing:", err)
	}
	t.Cleanup(func() { exec.Command("tc", "qdisc", "del", "dev", "lo", "root").Run() })
}

// setNetem replaces lo's qdisc: the emulated delay, plus the given loss options.
func setNetem(loss ...string) error {
	args := append([]string{"qdisc", "replace", "dev", "lo", "root", "netem", "delay", (netemRTT / 2).String()}, loss...)
	if out, err := exec.Command("tc", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tc %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func netem(t *testing.T, loss ...string) {
	t.Helper()
	if err := setNetem(loss...); err != nil {
		t.Fatal(err)
	}
}

// latencies runs lone requests on the idle connection, as a burst observatory probe does, and returns their times.
func (h *lossyHy2) latencies(t *testing.T, n int) []time.Duration {
	t.Helper()
	out := make([]time.Duration, 0, n)
	for i := range n {
		out = append(out, h.roundTrip(t, byte(i)))
		time.Sleep(20 * time.Millisecond)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// tail is the mean of the slowest tenth: the requests that met a loss.
func tail(sorted []time.Duration) time.Duration {
	worst := sorted[len(sorted)*9/10:]
	var sum time.Duration
	for _, d := range worst {
		sum += d
	}
	return sum / time.Duration(len(worst))
}

func percentile(sorted []time.Duration, p int) time.Duration { return sorted[len(sorted)*p/100] }

// tuned is spec024's first phase on both ends: the client turns Chrome parroting off so that it advertises the
// shorter max_ack_delay (spec021 R1).
func tuned(copies bool) *internet.QuicParams {
	p := &internet.QuicParams{MaxAckDelayMs: 5, InitialRttMs: 20, PtoBackoffLimit: 2, DisableChromeParrot: true}
	if copies {
		p.DuplicateMaxSize = 1500
		p.DuplicateTail = true
	}
	return p
}

// newNetemHy2 connects on the emulated path without loss, then turns the loss on.
func newNetemHy2(t *testing.T, clientParams, serverParams *internet.QuicParams, loss ...string) *lossyHy2 {
	t.Helper()
	netem(t)
	h := newLossyHy2(t, clientParams, serverParams)
	netem(t, loss...)
	return h
}

// slowFraction is the share of requests that met a loss: slower than the median by more than 10 ms.
func slowFraction(sorted []time.Duration) float64 {
	limit := percentile(sorted, 50) + 10*time.Millisecond
	slow := 0
	for _, d := range sorted {
		if d > limit {
			slow++
		}
	}
	return float64(slow) / float64(len(sorted))
}

// The random loss levels, each with its own seed: netem draws the same sequence for the three tunings of a level.
var randomLossLevels = []struct {
	loss, seed string
}{{"5%", "2111"}, {"10%", "2112"}, {"15%", "2113"}, {"20%", "2114"}}

// At a loss rate p a request meets a loss about 2p of the time (its request or its response); with a copy of every
// datagram on both ends only when both copies are lost, about 2p². The expectations hold at every level: the
// recovery parameters cut a loss's cost, so they gain more as double losses become common, and copies cut how
// often a request meets one.
func TestNetemRandomLossRecovery(t *testing.T) {
	requireNetem(t)
	const n = 200
	for _, level := range randomLossLevels {
		t.Run("loss="+level.loss, func(t *testing.T) {
			loss := []string{"loss", "random", level.loss, "seed", level.seed}
			run := func(client, server *internet.QuicParams) ([]time.Duration, internet.QuicCounters) {
				h := newNetemHy2(t, client, server, loss...)
				lat := h.latencies(t, n)
				return lat, h.stats()
			}
			plain, plainStats := run(nil, nil)
			fast, fastStats := run(tuned(false), tuned(false))
			copied, copiedStats := run(tuned(true), tuned(true))
			for _, r := range []struct {
				name string
				s    []time.Duration
			}{{"default", plain}, {"recovery parameters", fast}, {"with copies", copied}} {
				t.Logf("%s: p50 %s, p90 %s, p99 %s, slowest tenth %s, slowed %.1f%%", r.name, percentile(r.s, 50),
					percentile(r.s, 90), percentile(r.s, 99), tail(r.s), 100*slowFraction(r.s))
			}
			t.Logf("counters: default %+v; recovery parameters %+v; with copies %+v", plainStats, fastStats, copiedStats)

			// netem lost packets, and Hy2's BBR connection counted them, including the lone requests' packets that
			// only a PTO probe recovers (spec021 FR-009)
			if plainStats.PacketsLost == 0 || plainStats.PTOCount == 0 || fastStats.PacketsLost == 0 || fastStats.PTOCount == 0 {
				t.Errorf("losses and PTOs counted under BBR: default %+v, tuned %+v", plainStats, fastStats)
			}
			// a 5 ms ACK delay on both ends and the limited backoff recover a loss sooner
			if tail(plain)-tail(fast) < 12*time.Millisecond {
				t.Errorf("the slowest tenth: %s by default, %s with the recovery parameters; want at least 12 ms less", tail(plain), tail(fast))
			}
			// copies: a request meets a loss at most a third as often, and the slowest tenth is at least 25 ms faster
			if slowFraction(copied) > slowFraction(plain)/3 {
				t.Errorf("slowed by a loss: %.1f%% with copies, %.1f%% by default; want at most a third", 100*slowFraction(copied), 100*slowFraction(plain))
			}
			if tail(plain)-tail(copied) < 25*time.Millisecond {
				t.Errorf("the slowest tenth with copies: %s, by default %s; want at least 25 ms less", tail(copied), tail(plain))
			}
			if copiedStats.CopiesSent == 0 || fastStats.CopiesSent != 0 || plainStats.CopiesSent != 0 {
				t.Errorf("copies only where configured: %d, %d, %d", plainStats.CopiesSent, fastStats.CopiesSent, copiedStats.CopiesSent)
			}
		})
	}
}

func TestNetemBlackoutRecovery(t *testing.T) {
	requireNetem(t)
	const blackout = 300 * time.Millisecond
	// overshoot is how long after the path came back the request completed
	overshoot := func(params *internet.QuicParams) time.Duration {
		h := newNetemHy2(t, params, params)
		// the path drops everything for 300 ms while a request is outstanding; the end is when tc restored it
		netem(t, "loss", "random", "100%")
		start := time.Now()
		end := make(chan time.Duration, 1)
		go func() {
			time.Sleep(blackout)
			if err := setNetem(); err != nil {
				t.Error(err)
			}
			end <- time.Since(start)
		}()
		elapsed := h.roundTrip(t, 0xfc)
		ended := <-end
		t.Logf("blackout %s, request %s", ended, elapsed)
		return elapsed - ended
	}
	plain := overshoot(nil)
	limited := overshoot(tuned(false))
	t.Logf("after the blackout the request took %s more by default, %s more with ptoBackoffLimit 2 and a 5 ms ACK delay", plain, limited)
	// ptoBackoffLimit 2 caps the probe interval at 4 PTOs, about 80 ms on this path, then one round trip; the
	// doubling PTO waits up to the blackout's own length more
	if limited > 100*time.Millisecond {
		t.Errorf("with the limit the request completes within 4 PTOs and a round trip after the blackout, took %s", limited)
	}
	if plain-limited < 100*time.Millisecond {
		t.Errorf("after the blackout: %s by default, %s with the limit; want at least 100 ms between them", plain, limited)
	}
}
