package router

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/transport"
)

// benchManager selects by prefix among fixed outbounds, all freedom.
type benchManager struct {
	outbound.Manager
	tags []string
}

func (m *benchManager) Select(selectors []string) []string {
	var out []string
	for _, tag := range m.tags {
		if slices.ContainsFunc(selectors, func(s string) bool { return strings.HasPrefix(tag, s) }) {
			out = append(out, tag)
		}
	}
	return out
}

func (m *benchManager) GetHandler(tag string) outbound.Handler {
	if _, ok := slices.BinarySearch(m.tags, tag); ok {
		return &freedomHandler{tag: tag}
	}
	return nil
}

type freedomHandler struct{ tag string }

func (h *freedomHandler) Start() error                              { return nil }
func (h *freedomHandler) Close() error                              { return nil }
func (h *freedomHandler) Tag() string                               { return h.tag }
func (h *freedomHandler) Dispatch(context.Context, *transport.Link) {}
func (h *freedomHandler) SenderSettings() *serial.TypedMessage      { return nil }
func (h *freedomHandler) ProxySettings() *serial.TypedMessage {
	return serial.ToTypedMessage(&freedom.Config{})
}

// fleetGraph is E13's shape: 100 thresholdLeastPing balancers over 300 observed outbounds, chained
// three deep through their fallbacks (every third balancer falls back to the next, the last of each
// chain to an outbound).
func fleetGraph() (*Router, map[string]*Balancer, []*observatory.OutboundStatus) {
	var tags []string
	var statuses []*observatory.OutboundStatus
	for i := range 300 {
		tag := fmt.Sprintf("o%03d-%d", i/3, i%3)
		tags = append(tags, tag)
		rtt := time.Duration(20+i%90) * time.Millisecond
		statuses = append(statuses, &observatory.OutboundStatus{
			OutboundTag: tag, Alive: true, Delay: rtt.Milliseconds(),
			HealthPing: &observatory.HealthPingMeasurementResult{All: 25, Average: int64(rtt)},
		})
	}
	sort.Strings(tags)
	ohm := &benchManager{tags: tags}
	m := map[string]*Balancer{}
	for i := range 100 {
		fallback := fmt.Sprintf("o%03d-0", (i+1)%100)
		if i%3 != 2 {
			fallback = fmt.Sprintf("b%03d", i+1)
		}
		m[fmt.Sprintf("b%03d", i)] = &Balancer{
			tag: fmt.Sprintf("b%03d", i), selectors: []string{fmt.Sprintf("o%03d-", i)}, fallbackTag: fallback, ohm: ohm,
			strategy: &ThresholdLeastPingStrategy{policy: thresholdPolicy{maxRTT: 50 * time.Millisecond, recoverRTT: 40 * time.Millisecond}},
		}
	}
	return &Router{ohm: ohm}, m, statuses
}

// E13: the cost of one pass over the fleet-sized graph.
func BenchmarkThresholdPass(b *testing.B) {
	r, m, statuses := fleetGraph()
	b.ReportAllocs()
	for b.Loop() {
		r.computePass(m, statuses, "")
	}
}

// E13: p99 of one pass over the fleet-sized graph. A wall-clock bound only means something on an idle
// machine, so the 1ms target is checked when XRAY_SPEC027_TIMING=1 asks for it (run alone, without
// -race); otherwise the times are only logged.
func TestThresholdPassTime(t *testing.T) {
	r, m, statuses := fleetGraph()
	if st := r.computePass(m, statuses, "").state("b000"); st == nil || st.decision.mode == modePending {
		t.Fatalf("the fleet graph did not compute: %+v", st)
	}
	times := make([]time.Duration, 1000)
	for i := range times {
		start := time.Now()
		r.computePass(m, statuses, "")
		times[i] = time.Since(start)
	}
	slices.Sort(times)
	p50, p99 := times[len(times)/2], times[len(times)*99/100]
	t.Logf("E13: one pass over 100 balancers and 300 outbounds: p50 %v, p99 %v", p50, p99)
	if os.Getenv("XRAY_SPEC027_TIMING") == "1" && !raceEnabled && p99 > time.Millisecond {
		t.Fatalf("E13: p99 %v over the 1ms target", p99)
	}
}
