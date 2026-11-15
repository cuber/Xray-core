package command_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
)

// Core spec027 Control Plane (R cases) and E17 over a real Core: a real burst observatory probes local
// targets whose delay and failure the test sets; every read goes through RoutingService over gRPC.

type probeTarget struct {
	delay   atomic.Int64
	fail    atomic.Bool
	release chan struct{}
	server  *httptest.Server
}

func newProbeTarget(t *testing.T, delay time.Duration) *probeTarget {
	p := &probeTarget{release: make(chan struct{})}
	p.delay.Store(int64(delay))
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-p.release
		if p.fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		time.Sleep(time.Duration(p.delay.Load()))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *probeTarget) set(delay time.Duration) { p.fail.Store(false); p.delay.Store(int64(delay)) }

type rpcFixture struct {
	routing RoutingServiceClient
	a, b, f *probeTarget
}

func newRPCFixture(t *testing.T, interval string, balancers []any) *rpcFixture {
	f := &rpcFixture{a: newProbeTarget(t, 10*time.Millisecond), b: newProbeTarget(t, 20*time.Millisecond), f: newProbeTarget(t, 5*time.Millisecond)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	outbounds := []any{map[string]any{"tag": "sink", "protocol": "blackhole"}}
	for _, tag := range []string{"a-hy2", "a-vless", "f-backup"} {
		outbounds = append(outbounds, map[string]any{"tag": tag, "protocol": "freedom"})
	}
	raw, err := json.Marshal(map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort), "services": []string{"RoutingService"},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"balancers": balancers,
			"rules":     []any{map[string]any{"type": "field", "domain": []string{"full:t.test"}, "balancerTag": "t"}},
		},
		"burstObservatory": map[string]any{"pingGroups": []any{map[string]any{
			"subjectSelector": []string{"a-", "f-"},
			"pingConfig": map[string]any{
				"destination": f.a.server.URL,
				"destinationsByPrefix": map[string]string{
					"a-hy2": f.a.server.URL, "a-vless": f.b.server.URL, "f-backup": f.f.server.URL,
				},
				"interval": interval, "sampling": 1, "timeout": "2s",
			},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f.routing = NewRoutingServiceClient(conn)
	return f
}

func (f *rpcFixture) releaseAll() {
	for _, p := range []*probeTarget{f.a, f.b, f.f} {
		close(p.release)
	}
}

func threshold(tag string, selector []string, fallback, maxRTT, recoverRTT string) map[string]any {
	settings := map[string]any{"maxRTT": maxRTT}
	if recoverRTT != "" {
		settings["recoverRTT"] = recoverRTT
	}
	b := map[string]any{"tag": tag, "selector": selector, "strategy": map[string]any{"type": "thresholdLeastPing", "settings": settings}}
	if fallback != "" {
		b["fallbackTag"] = fallback
	}
	return b
}

func (f *rpcFixture) info(t *testing.T, tag string) *BalancerMsg {
	t.Helper()
	resp, err := f.routing.GetBalancerInfo(call(t), &GetBalancerInfoRequest{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Balancer
}

// waitFor polls a balancer until cond holds, with a bounded deadline.
func (f *rpcFixture) waitFor(t *testing.T, tag, what string, cond func(*BalancerMsg) bool) *BalancerMsg {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		msg := f.info(t, tag)
		if cond(msg) {
			return msg
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s not reached; last %v", tag, what, msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func member(msg *BalancerMsg, tag string) *BalancerMember {
	for _, m := range msg.Members {
		if m.Tag == tag {
			return m
		}
	}
	return nil
}

// consistent checks R07/R13: within one response the decision, shares, principle target and fallback
// flag agree with one another.
func consistent(t *testing.T, msg *BalancerMsg) {
	t.Helper()
	d := msg.ThresholdDecision
	var picked []string
	for _, m := range msg.Members {
		if m.Share > 0 {
			picked = append(picked, m.Tag)
		}
	}
	switch d.Mode {
	case "primary", "degraded":
		if msg.Fallback.Active {
			if len(picked) != 0 || len(msg.PrincipleTarget.Tag) != 0 || d.ChoiceTag != msg.Fallback.Tag {
				t.Fatalf("R13 %s fallback: picked %v principle %v choice %s", d.Mode, picked, msg.PrincipleTarget.Tag, d.ChoiceTag)
			}
			return
		}
		if len(picked) != 1 || picked[0] != d.ChoiceTag || len(msg.PrincipleTarget.Tag) != 1 || msg.PrincipleTarget.Tag[0] != d.ChoiceTag {
			t.Fatalf("R13 %s: picked %v principle %v choice %s", d.Mode, picked, msg.PrincipleTarget.Tag, d.ChoiceTag)
		}
		if c := member(msg, d.ChoiceTag); d.Mode == "primary" && !c.Selection.WithinLimit {
			t.Fatalf("R07: primary choice %s not within the limit", d.ChoiceTag)
		}
	case "fallback":
		if !msg.Fallback.Active || len(picked) != 0 || len(msg.PrincipleTarget.Tag) != 0 || !d.FallbackSelection.WithinLimit {
			t.Fatalf("R13 fallback: active %v picked %v principle %v", msg.Fallback.Active, picked, msg.PrincipleTarget.Tag)
		}
	}
}

func TestThresholdLeastPingOverRPC(t *testing.T) {
	f := newRPCFixture(t, "1s", []any{
		threshold("t", []string{"a-"}, "f-backup", "50ms", "40ms"),
		threshold("u", []string{"f-"}, "sink", "50ms", ""),
		map[string]any{"tag": "rr", "selector": []string{"a-"}, "strategy": map[string]any{"type": "roundRobin"}, "fallbackTag": "sink"},
		threshold("q", []string{"f-"}, "rr", "1ms", ""),
		map[string]any{"tag": "l", "selector": []string{"a-"}, "strategy": map[string]any{"type": "leastPing"}, "fallbackTag": "sink"},
	})
	// R05 pending: every probe waits on its target.
	msg := f.info(t, "t")
	if msg.Strategy != "thresholdleastping" || msg.ThresholdPolicy.GetMaxRttNs() != int64(50*time.Millisecond) ||
		msg.ThresholdPolicy.GetRecoverRttNs() != int64(40*time.Millisecond) {
		t.Fatalf("R01: %s %v", msg.Strategy, msg.ThresholdPolicy)
	}
	if d := msg.ThresholdDecision; d.Mode != "pending" || d.Terminal != "drop" || msg.Status.Known ||
		member(msg, "a-hy2").Selection.Reason != "unobserved" || member(msg, "a-hy2").Selection.RttKnown {
		t.Fatalf("R05 pending: %v", msg)
	}
	f.releaseAll()
	msg = f.waitFor(t, "t", "primary a-hy2", func(m *BalancerMsg) bool {
		return m.ThresholdDecision.Mode == "primary" && m.ThresholdDecision.ChoiceTag == "a-hy2"
	})
	consistent(t, msg)
	if s := member(msg, "a-vless").Selection; !s.WithinLimit || s.Reason != "eligible" || member(msg, "a-vless").Share != 0 {
		t.Fatalf("R05 eligible loser: %v", s)
	}
	// R02: alive but over the limit.
	f.a.set(120 * time.Millisecond)
	msg = f.waitFor(t, "t", "a-hy2 over", func(m *BalancerMsg) bool { return m.ThresholdDecision.ChoiceTag == "a-vless" })
	consistent(t, msg)
	if a := member(msg, "a-hy2"); !a.Status.Alive || a.Selection.WithinLimit || !a.Selection.Usable ||
		a.Selection.Reason != "max_rtt_exceeded" || !a.Selection.Latched {
		t.Fatalf("R02: %v %v", a.Status, a.Selection)
	}
	// R03: latched between recoverRTT and maxRTT.
	f.a.set(44 * time.Millisecond)
	msg = f.waitFor(t, "t", "a-hy2 recovering", func(m *BalancerMsg) bool {
		s := member(m, "a-hy2").Selection
		return s.Reason == "recovering" && s.Latched && !s.WithinLimit
	})
	consistent(t, msg)
	// R13: the fallback.
	f.a.set(120 * time.Millisecond)
	f.b.set(150 * time.Millisecond)
	msg = f.waitFor(t, "t", "fallback", func(m *BalancerMsg) bool { return m.ThresholdDecision.Mode == "fallback" })
	consistent(t, msg)
	// R04/R13: degraded, the fastest usable option above the limit.
	f.f.set(200 * time.Millisecond)
	msg = f.waitFor(t, "t", "degraded a-hy2", func(m *BalancerMsg) bool {
		return m.ThresholdDecision.Mode == "degraded" && m.ThresholdDecision.ChoiceTag == "a-hy2"
	})
	consistent(t, msg)
	if a := member(msg, "a-hy2"); a.Share != 1 || a.Selection.WithinLimit {
		t.Fatalf("R04: %v", a)
	}
	// R14: q's fallback rr is a mean of a-hy2 and a-vless, with its coverage.
	msg = f.info(t, "q")
	if s := msg.ThresholdDecision.FallbackSelection; s == nil || s.Aggregation != "mean" || s.Measured != 2 || s.Dispatchable != 2 {
		t.Fatalf("R14: %v", s)
	}
	// R10: leastPing carries none of the new fields.
	if l := f.info(t, "l"); l.ThresholdPolicy != nil || l.ThresholdDecision != nil || l.Members[0].Selection != nil {
		t.Fatalf("R10: %v", l)
	}
	// R11: nothing usable: u ends on its blackhole; an override reports its target.
	f.f.fail.Store(true)
	msg = f.waitFor(t, "u", "blackhole", func(m *BalancerMsg) bool { return m.ThresholdDecision.Terminal == "blackhole" })
	if d := msg.ThresholdDecision; d.Mode != "unavailable" || d.ChoiceTag != "sink" || !msg.Fallback.Active || msg.Members[0].Share != 0 {
		t.Fatalf("R11 blackhole: %v", msg)
	}
	if _, err := f.routing.OverrideBalancerTarget(call(t), &OverrideBalancerTargetRequest{BalancerTag: "u", Target: "f-backup"}); err != nil {
		t.Fatal(err)
	}
	msg = f.waitFor(t, "u", "override", func(m *BalancerMsg) bool { return m.ThresholdDecision.Mode == "override" })
	if msg.ThresholdDecision.ChoiceTag != "f-backup" || member(msg, "f-backup").Share != 1 || member(msg, "f-backup").Status.Alive {
		t.Fatalf("R11 override: %v", msg)
	}
	// R15: one listing, one pass.
	list, err := f.routing.ListBalancers(call(t), &ListBalancersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	generations := map[uint64]bool{}
	for _, item := range list.Balancers {
		if d := item.Balancer.ThresholdDecision; d != nil {
			generations[d.Generation] = true
			if d.ObservedAtMs == 0 {
				t.Fatalf("R15: %s has no observation time", item.Tag)
			}
		}
	}
	if len(generations) != 1 {
		t.Fatalf("R15: generations %v", generations)
	}
}

// R07, R08: reads stay consistent while observations, overrides and rules change, under -race.
func TestThresholdLeastPingRPCUnderChange(t *testing.T) {
	f := newRPCFixture(t, "1s", []any{threshold("t", []string{"a-"}, "f-backup", "50ms", "40ms")})
	f.releaseAll()
	f.waitFor(t, "t", "a decision", func(m *BalancerMsg) bool { return m.ThresholdDecision.Mode != "pending" })
	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				fn(i)
			}
		}()
	}
	loop(func(i int) {
		f.a.set(time.Duration(30+i%3*50) * time.Millisecond)
		time.Sleep(20 * time.Millisecond)
	})
	loop(func(i int) {
		_, _ = f.routing.OverrideBalancerTarget(call(t), &OverrideBalancerTargetRequest{BalancerTag: "t", Target: []string{"", "a-vless"}[i%2]})
	})
	loop(func(i int) {
		_, _ = f.routing.AddRule(call(t), &AddRuleRequest{ShouldAppend: true, Config: serial.ToTypedMessage(&router.Config{
			BalancingRule: []*router.BalancingRule{{
				Tag: fmt.Sprint("x", i), OutboundSelector: []string{"a-"}, Strategy: "thresholdleastping", FallbackTag: "sink",
				StrategySettings: serial.ToTypedMessage(&router.StrategyThresholdLeastPingConfig{MaxRTT: int64(50 * time.Millisecond)}),
			}},
			Rule: []*router.RoutingRule{{
				RuleTag: fmt.Sprint("x", i), TargetTag: &router.RoutingRule_BalancingTag{BalancingTag: fmt.Sprint("x", i)},
				InboundTag: []string{"none"},
			}},
		})})
		_, _ = f.routing.RemoveRule(call(t), &RemoveRuleRequest{RuleTag: fmt.Sprint("x", i)})
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msg := f.info(t, "t"); msg.ThresholdDecision.Mode != "override" {
			consistent(t, msg)
		}
	}
	close(stop)
	wg.Wait()
}

// E17: with a one-hour interval the decision leaves pending as soon as the immediate first checks
// return, without waiting for a round.
func TestThresholdLeastPingFirstChecks(t *testing.T) {
	f := newRPCFixture(t, "1h", []any{threshold("t", []string{"a-"}, "f-backup", "50ms", "")})
	if d := f.info(t, "t").ThresholdDecision; d.Mode != "pending" {
		t.Fatalf("E17 before release: %v", d)
	}
	f.releaseAll()
	msg := f.waitFor(t, "t", "primary", func(m *BalancerMsg) bool { return m.ThresholdDecision.Mode == "primary" })
	if msg.ThresholdDecision.ChoiceTag != "a-hy2" {
		t.Fatalf("E17: %v", msg.ThresholdDecision)
	}
}

// R09: the new fields are additive. Dropping fields 11 and 12 of BalancerMsg and 6 of BalancerMember
// from a new response leaves exactly the message an older Core sends, which an older client decodes.
func TestThresholdFieldsAreAdditive(t *testing.T) {
	full := &BalancerMsg{
		Strategy: "thresholdleastping", Status: &BalancerStatus{Known: true, Alive: true, Delay: 12, Provenance: true},
		Members:           []*BalancerMember{{Tag: "a", Share: 1, Status: &BalancerStatus{Known: true}, Selection: &ThresholdSelection{Usable: true, Reason: "eligible"}}},
		ThresholdPolicy:   &ThresholdPolicy{MaxRttNs: 1},
		ThresholdDecision: &ThresholdDecision{Mode: "primary"},
	}
	raw, err := proto.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	legacy := proto.Clone(full).(*BalancerMsg)
	legacy.ThresholdPolicy, legacy.ThresholdDecision, legacy.Members[0].Selection = nil, nil, nil
	var old BalancerMsg
	if err := proto.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	old.ThresholdPolicy, old.ThresholdDecision = nil, nil
	old.Members[0].Selection = nil
	if !proto.Equal(&old, legacy) {
		t.Fatalf("R09: legacy fields changed: %v vs %v", &old, legacy)
	}
	// The field numbers are the ones the spec reserves.
	for _, c := range []struct {
		msg   proto.Message
		field string
		num   protoreflect.FieldNumber
	}{
		{&BalancerMsg{}, "threshold_policy", 11}, {&BalancerMsg{}, "threshold_decision", 12}, {&BalancerMember{}, "selection", 6},
	} {
		fd := c.msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(c.field))
		if fd == nil || fd.Number() != c.num {
			t.Fatalf("R09: %s is %v", c.field, fd)
		}
	}
}
