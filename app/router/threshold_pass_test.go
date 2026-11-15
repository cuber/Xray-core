package router

import (
	"context"
	goerrors "errors"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/freedom"
)

// Core spec027 T004/T005: passes computed from observation changes, through a real Core whose
// observatory is a fixture that serves injected statuses, notifies every injection and counts reads.

type fixtureObservatory struct {
	mu        sync.Mutex
	statuses  map[string]*observatory.OutboundStatus
	observed  []string
	reads     atomic.Int64
	wrongType atomic.Bool
	shared    atomic.Bool  // hand out the stored statuses themselves (E21)
	delay     atomic.Int64 // slow each read down by this many nanoseconds (E07)
	changes   signal.Broadcast
}

// fixtureNext is the observatory the next core.New creates: the test registers ObservationResult,
// which no feature uses as a config, as the fixture's config type, so the fixture is in place for
// core.New's start check and for the strategies' feature requirements.
var fixtureNext atomic.Pointer[fixtureObservatory]

func init() {
	common.Must(common.RegisterConfig((*observatory.ObservationResult)(nil), func(context.Context, interface{}) (interface{}, error) {
		return fixtureNext.Load(), nil
	}))
}

func newFixtureObservatory(observed ...string) *fixtureObservatory {
	return &fixtureObservatory{statuses: map[string]*observatory.OutboundStatus{}, observed: observed}
}

func (*fixtureObservatory) Type() interface{} { return extension.ObservatoryType() }
func (*fixtureObservatory) Start() error      { return nil }
func (*fixtureObservatory) Close() error      { return nil }

func (o *fixtureObservatory) GetObservation(context.Context) (proto.Message, error) {
	o.reads.Add(1)
	if d := o.delay.Load(); d > 0 {
		time.Sleep(time.Duration(d))
	}
	if o.wrongType.Load() {
		return &observatory.OutboundStatus{}, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := &observatory.ObservationResult{}
	for _, tag := range slices.Sorted(maps.Keys(o.statuses)) {
		s := o.statuses[tag]
		if !o.shared.Load() {
			s = proto.Clone(s).(*observatory.OutboundStatus)
		}
		out.Status = append(out.Status, s)
	}
	return out, nil
}

func (o *fixtureObservatory) Observes(tag string) bool {
	return slices.ContainsFunc(o.observed, func(p string) bool { return strings.HasPrefix(tag, p) })
}

func (o *fixtureObservatory) OnChange(fn func()) func() { return o.changes.Subscribe(fn) }

// set injects one status (nil removes it) and notifies, as an observatory does after a result.
func (o *fixtureObservatory) set(tag string, s *observatory.OutboundStatus) {
	o.mu.Lock()
	if s == nil {
		delete(o.statuses, tag)
	} else {
		s.OutboundTag = tag
		o.statuses[tag] = s
	}
	o.mu.Unlock()
	o.changes.Fire()
}

func alive(rtt time.Duration) *observatory.OutboundStatus {
	return &observatory.OutboundStatus{
		Alive: true, Delay: rtt.Milliseconds(),
		HealthPing: &observatory.HealthPingMeasurementResult{All: 10, Average: int64(rtt), Min: int64(rtt), Max: int64(rtt)},
	}
}

func dead() *observatory.OutboundStatus {
	return &observatory.OutboundStatus{Alive: false, LastErrorReason: "probe failed", HealthPing: &observatory.HealthPingMeasurementResult{All: 10, Fail: 10}}
}

type passFixture struct {
	r        *Router
	obs      *fixtureObservatory
	instance *core.Instance
}

// startPassCore runs a Core with freedom outbounds a-hy2, a-vless, b-hy2, f-backup and direct, the
// blackhole sink, the fixture observatory observing a-, b- and f-, and config. Nothing dispatches.
func startPassCore(t *testing.T, config *Config) *passFixture {
	t.Helper()
	outbounds := []*core.OutboundHandlerConfig{{Tag: "sink", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})}}
	for _, tag := range []string{"a-hy2", "a-vless", "b-hy2", "f-backup", "direct"} {
		outbounds = append(outbounds, &core.OutboundHandlerConfig{Tag: tag, ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
	}
	obs := newFixtureObservatory("a-", "b-", "f-")
	fixtureNext.Store(obs)
	instance, err := core.New(&core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&observatory.ObservationResult{}),
			serial.ToTypedMessage(config),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		},
		Outbound: outbounds,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	return &passFixture{r: instance.GetFeature(routing.RouterType()).(*Router), obs: obs, instance: instance}
}

func ruleTo(balancer string) *RoutingRule { return chainRule(balancer) }

func thresholdOver(tag string, selector []string, fallback string, maxRTT, recoverRTT time.Duration) *BalancingRule {
	rule := thresholdRule(tag, fallback, maxRTT, recoverRTT)
	rule.OutboundSelector = selector
	return rule
}

func (f *passFixture) pass(t *testing.T, tag string) (*tlpPass, *tlpState) {
	t.Helper()
	b, ok := (*f.r.balancers.Load())[tag]
	if !ok {
		t.Fatalf("no balancer %s", tag)
	}
	p := b.pass.Load()
	st := p.state(tag)
	if st == nil {
		t.Fatalf("no state for %s", tag)
	}
	return p, st
}

func (f *passFixture) route(t *testing.T) string {
	t.Helper()
	tag, err := pickThresholdRoute(f.r)
	if err != nil {
		if goerrors.Is(err, routing.ErrBalancerUnavailable) {
			return "drop"
		}
		t.Fatalf("pick: %v", err)
	}
	return tag
}

func basicConfig() *Config {
	return &Config{
		Rule:          []*RoutingRule{ruleTo("t")},
		BalancingRule: []*BalancingRule{thresholdOver("t", []string{"a-"}, "f-backup", ms(50), ms(40))},
	}
}

// E01, E06, Q26 (pending, primary, override, unavailable): passes follow injected observations with no
// traffic; the reported status follows the TL-3 table.
func TestThresholdPassFollowsObservations(t *testing.T) {
	f := startPassCore(t, basicConfig())
	_, st := f.pass(t, "t")
	if st.decision.mode != modePending || st.bubbled != nil || !st.result.pending {
		t.Fatalf("start: mode %s bubbled %v", st.decision.mode, st.bubbled)
	}
	if got := f.route(t); got != "drop" {
		t.Fatalf("route while pending = %s", got)
	}
	f.obs.set("f-backup", alive(ms(10)))
	f.obs.set("a-hy2", alive(ms(30)))
	f.obs.set("a-vless", alive(ms(35)))
	p1, st := f.pass(t, "t")
	if st.decision.mode != modePrimary || st.decision.choice != "a-hy2" || f.route(t) != "a-hy2" {
		t.Fatalf("primary: %s %s", st.decision.mode, st.decision.choice)
	}
	if st.bubbled.GetOutboundTag() != "t" || st.bubbled.GetHealthPing().GetAverage() != int64(ms(30)) || st.result.rtt != ms(30) {
		t.Fatalf("Q26 primary reported %v / %+v", st.bubbled, st.result)
	}
	f.obs.set("a-hy2", alive(ms(80)))
	p2, st := f.pass(t, "t")
	if p2.gen <= p1.gen || st.decision.choice != "a-vless" || f.route(t) != "a-vless" {
		t.Fatalf("E01: gen %d -> %d, choice %s", p1.gen, p2.gen, st.decision.choice)
	}
	if err := f.r.SetOverrideTarget("t", "a-hy2"); err != nil {
		t.Fatal(err)
	}
	p3, st := f.pass(t, "t")
	if p3.gen <= p2.gen || st.override != "a-hy2" || st.result.rtt != ms(80) || f.route(t) != "a-hy2" {
		t.Fatalf("E06/Q26 override: gen %d override %q result %+v", p3.gen, st.override, st.result)
	}
	if err := f.r.SetOverrideTarget("t", ""); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"a-hy2", "a-vless", "f-backup"} {
		f.obs.set(tag, dead())
	}
	_, st = f.pass(t, "t")
	if st.decision.mode != modeUnavailable || st.decision.terminal != terminalDrop || st.bubbled.GetAlive() ||
		!strings.Contains(st.bubbled.GetLastErrorReason(), "no usable option") || f.route(t) != "drop" {
		t.Fatalf("Q26 unavailable: %s %v", st.decision.mode, st.bubbled)
	}
	b := (*f.r.balancers.Load())["t"]
	if b.drops.Load() < 2 || *b.lastDrop.Load() == "" {
		t.Fatalf("D14 drops %d", b.drops.Load())
	}
}

// Q25 at the router level: an unexpected observation type makes every option unknown.
func TestThresholdPassWithAnUnusableObservation(t *testing.T) {
	f := startPassCore(t, basicConfig())
	f.obs.set("a-hy2", alive(ms(10)))
	f.obs.wrongType.Store(true)
	f.obs.set("a-vless", alive(ms(10)))
	_, st := f.pass(t, "t")
	if st.decision.mode != modeUnavailable || st.decision.reason != "unexpected observation type" ||
		st.decision.candidates[0].reason != reasonObservationUnavailable {
		t.Fatalf("Q25: %s %q %+v", st.decision.mode, st.decision.reason, st.decision.candidates)
	}
}

// Y06/C12, Y07, Y11 at the router level.
func TestThresholdLatchesAcrossPassesAndPublications(t *testing.T) {
	f := startPassCore(t, &Config{
		Rule:          []*RoutingRule{ruleTo("t")},
		BalancingRule: []*BalancingRule{thresholdOver("t", []string{"a-hy2", "b-"}, "f-backup", ms(50), ms(40))},
	})
	f.obs.set("f-backup", alive(ms(30)))
	f.obs.set("b-hy2", alive(ms(70)))
	f.obs.set("a-hy2", alive(ms(60)))
	p, st := f.pass(t, "t")
	if !st.latches["a-hy2"] || st.decision.choice != "f-backup" {
		t.Fatalf("latched %v choice %s", st.latches, st.decision.choice)
	}
	// Y07: picks and reads change neither latches nor passes.
	before := fmt.Sprint(st.latches)
	for range 1000 {
		f.route(t)
		_, _ = f.r.GetMembers("t")
		_, _, _, _ = f.r.GetFallback("t")
	}
	if p2, st2 := f.pass(t, "t"); p2 != p || fmt.Sprint(st2.latches) != before {
		t.Fatalf("Y07: a pick or a read changed the pass")
	}
	// Y11: an override neither consults nor changes the latch.
	if err := f.r.SetOverrideTarget("t", "b-hy2"); err != nil {
		t.Fatal(err)
	}
	f.obs.set("a-hy2", alive(ms(45)))
	if err := f.r.SetOverrideTarget("t", ""); err != nil {
		t.Fatal(err)
	}
	if _, st := f.pass(t, "t"); !st.latches["a-hy2"] || f.route(t) != "f-backup" {
		t.Fatalf("Y11: latches %v route %s", st.latches, f.route(t))
	}
	// Y06/C12: a publication builds a new balancer whose latches start released and whose limit is new.
	if err := f.r.ReloadRules(&Config{
		Rule:          []*RoutingRule{ruleTo("t")},
		BalancingRule: []*BalancingRule{thresholdOver("t", []string{"a-hy2", "b-"}, "f-backup", ms(50), ms(40))},
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, st := f.pass(t, "t"); st.latches["a-hy2"] || f.route(t) != "a-hy2" {
		t.Fatalf("Y06: latches %v route %s", st.latches, f.route(t))
	}
	// An append keeps an existing balancer, and its latches, as they are.
	f.obs.set("a-hy2", alive(ms(60)))
	if err := f.r.ReloadRules(&Config{BalancingRule: []*BalancingRule{
		thresholdOver("u", []string{"b-"}, "sink", ms(50), 0),
	}}, true); err != nil {
		t.Fatal(err)
	}
	if _, st := f.pass(t, "t"); !st.latches["a-hy2"] {
		t.Fatalf("append reset t's latches: %v", st.latches)
	}
}

// E05: during publications under concurrent picks, no pick sees a map without a decision.
func TestThresholdPublicationNeverLeavesAMapWithoutAPass(t *testing.T) {
	f := startPassCore(t, basicConfig())
	f.obs.set("a-hy2", alive(ms(10)))
	f.obs.set("a-vless", alive(ms(20)))
	f.obs.set("f-backup", alive(ms(10)))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Value
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if tag, err := pickThresholdRoute(f.r); err != nil || tag != "a-hy2" {
					bad.Store(fmt.Sprintf("%q %v", tag, err))
				}
			}
		}()
	}
	for i := range 200 {
		config := basicConfig()
		if i%2 == 1 {
			config.BalancingRule = append(config.BalancingRule, thresholdOver("u", []string{"b-"}, "sink", ms(50), 0))
		}
		if err := f.r.ReloadRules(config, false); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if v := bad.Load(); v != nil {
		t.Fatalf("E05: a pick saw %v", v)
	}
}

// E07, E08, E11: coalesced passes, no observation read by picks, one read per pass.
func TestThresholdPassCost(t *testing.T) {
	f := startPassCore(t, basicConfig())
	f.obs.set("a-hy2", alive(ms(10)))
	reads := f.obs.reads.Load()
	for range 10000 {
		f.route(t)
	}
	if got := f.obs.reads.Load(); got != reads {
		t.Fatalf("E08: picks read the observation %d times", got-reads)
	}

	goroutines := runtime.NumGoroutine()
	p0, _ := f.pass(t, "t")
	f.obs.delay.Store(int64(2 * time.Millisecond))
	var wg sync.WaitGroup
	for i := range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.obs.set("a-vless", alive(ms(float64(20+i%3))))
		}()
	}
	wg.Wait()
	f.obs.delay.Store(0)
	f.obs.set("a-vless", alive(ms(5)))
	p1, st := f.pass(t, "t")
	if passes := p1.gen - p0.gen; passes >= 1001 || st.decision.choice != "a-vless" {
		t.Fatalf("E07: %d passes for 1001 notifications, choice %s", passes, st.decision.choice)
	}
	if extra := runtime.NumGoroutine() - goroutines; extra > 2 {
		t.Fatalf("E07: %d more goroutines", extra)
	}

	for _, n := range []int{1, 10, 100} {
		config := &Config{}
		for i := range n {
			config.BalancingRule = append(config.BalancingRule, thresholdOver(fmt.Sprint("t", i), []string{"a-"}, "f-backup", ms(50), 0))
		}
		if err := f.r.ReloadRules(config, false); err != nil {
			t.Fatal(err)
		}
		r0, g0 := f.obs.reads.Load(), (*f.r.balancers.Load())["t0"].pass.Load().gen
		f.obs.set("a-hy2", alive(ms(12)))
		r1, g1 := f.obs.reads.Load(), (*f.r.balancers.Load())["t0"].pass.Load().gen
		if r1-r0 != int64(g1-g0) {
			t.Fatalf("E11: %d balancers, %d reads for %d passes", n, r1-r0, g1-g0)
		}
	}
}

// E10: without a thresholdLeastPing balancer there is no subscription and no pass.
func TestThresholdIdleWithoutThresholdBalancers(t *testing.T) {
	f := startPassCore(t, &Config{BalancingRule: []*BalancingRule{
		{Tag: "l", OutboundSelector: []string{"a-"}, Strategy: "leastping", FallbackTag: "sink"},
	}})
	f.obs.set("a-hy2", alive(ms(10)))
	if f.r.tlpSubscribed || f.r.tlpGen.Load() != 0 || (*f.r.balancers.Load())["l"].pass.Load() != nil {
		t.Fatalf("E10: subscribed %v passes %d", f.r.tlpSubscribed, f.r.tlpGen.Load())
	}
}

// E12, N15, N17, N19-N23 (decision level): children first in one pass; reported statuses; means.
func TestThresholdNesting(t *testing.T) {
	f := startPassCore(t, &Config{
		Rule: []*RoutingRule{ruleTo("p")},
		BalancingRule: []*BalancingRule{
			thresholdOver("p", []string{"a-"}, "c", ms(50), 0),
			thresholdOver("c", []string{"b-"}, "f-backup", ms(200), 0),
			{Tag: "rr", OutboundSelector: []string{"a-hy2", "b-hy2"}, Strategy: "roundrobin", FallbackTag: "sink"},
			thresholdOver("q", []string{"f-"}, "rr", ms(50), 0),
			{Tag: "legacy", OutboundSelector: []string{"f-"}, Balancers: []string{"c"}, Strategy: "leastping", FallbackTag: "sink"},
		},
	})
	f.obs.set("a-hy2", alive(ms(80)))
	f.obs.set("a-vless", alive(ms(90)))
	f.obs.set("f-backup", alive(ms(300)))
	f.obs.set("b-hy2", alive(ms(40)))
	_, st := f.pass(t, "p")
	if st.decision.mode != modeFallback || st.decision.choice != "c" || f.route(t) != "b-hy2" {
		t.Fatalf("N09: %s %s route %s", st.decision.mode, st.decision.choice, f.route(t))
	}
	// E12: the child's change reaches the parent in the same pass. N11/N13: c's own 200ms limit
	// lets 120 through; p's 50ms does not.
	f.obs.set("b-hy2", alive(ms(120)))
	p, st := f.pass(t, "p")
	child := p.state("c")
	if child.decision.mode != modePrimary || child.result.rtt != ms(120) || st.decision.fallbackOf.rtt != ms(120) ||
		st.decision.mode != modeDegraded || st.decision.choice != "a-hy2" {
		t.Fatalf("E12/N11: child %s %v parent %s %s fallback %+v", child.decision.mode, child.result.rtt,
			st.decision.mode, st.decision.choice, st.decision.fallbackOf)
	}
	// N15: a legacy leastPing parent bubbles c's decision (b-hy2 120ms), not its fastest candidate.
	statuses := []*observatory.OutboundStatus{alive(ms(80)), alive(ms(120)), alive(ms(300))}
	statuses[0].OutboundTag, statuses[1].OutboundTag, statuses[2].OutboundTag = "a-hy2", "b-hy2", "f-backup"
	reader := newStatusReader(*f.r.balancers.Load(), statuses, f.r.ohm)
	if s := reader.balancer("c"); s.GetHealthPing().GetAverage() != int64(ms(120)) || s.GetOutboundTag() != "c" {
		t.Fatalf("N15: legacy read of c %v", s)
	}
	// N12/N23: q's fallback rr is a roundRobin of a-hy2 80 and b-hy2 120: mean 100, not 80.
	_, q := f.pass(t, "q")
	if fbo := q.decision.fallbackOf; fbo == nil || fbo.rtt != ms(100) || fbo.mean == nil || fbo.mean.measured != 2 {
		t.Fatalf("N12: mean %+v", fbo)
	}
	if q.decision.mode != modeDegraded || q.decision.choice != "rr" {
		t.Fatalf("N23: q %s %s (f-backup 300 vs mean 100)", q.decision.mode, q.decision.choice)
	}
	// N17: no usable measured member: rr's own fallback (a blackhole) bubbles, not a fabricated mean.
	f.obs.set("a-hy2", dead())
	f.obs.set("b-hy2", dead())
	_, q = f.pass(t, "q")
	if fbo := q.decision.fallbackOf; fbo.usable || fbo.mean != nil {
		t.Fatalf("N17: %+v", fbo)
	}
	// N21: an overridden thresholdLeastPing child reports its target; the parent applies its own limit.
	f.obs.set("a-hy2", alive(ms(80)))
	f.obs.set("b-hy2", alive(ms(20)))
	if err := f.r.SetOverrideTarget("c", "f-backup"); err != nil {
		t.Fatal(err)
	}
	p, st = f.pass(t, "p")
	if fbo := st.decision.fallbackOf; fbo.rtt != ms(300) || fbo.withinLimit {
		t.Fatalf("N21: parent reads %+v", fbo)
	}
	// N20: a child that chose its own fallback reports the fallback's RTT.
	if err := f.r.SetOverrideTarget("c", ""); err != nil {
		t.Fatal(err)
	}
	f.obs.set("b-hy2", alive(ms(250)))
	f.obs.set("f-backup", alive(ms(40)))
	p, _ = f.pass(t, "p")
	if c := p.state("c"); c.decision.choice != "f-backup" || c.result.rtt != ms(40) {
		t.Fatalf("N20: child %s reports %v", c.decision.choice, c.result.rtt)
	}
}

// E20: runtime handler changes publish passes; a removed fallback is absent, an added candidate pending.
func TestThresholdFollowsHandlerChanges(t *testing.T) {
	f := startPassCore(t, basicConfig())
	f.obs.set("a-hy2", alive(ms(10)))
	f.obs.set("f-backup", alive(ms(10)))
	ohm := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	h, err := core.CreateObject(f.instance, &core.OutboundHandlerConfig{Tag: "a-new", ProxySettings: serial.ToTypedMessage(&blackhole.Config{})})
	if err != nil {
		t.Fatal(err)
	}
	p0, _ := f.pass(t, "t")
	if err := ohm.AddHandler(context.Background(), h.(outbound.Handler)); err != nil {
		t.Fatal(err)
	}
	p1, st := f.pass(t, "t")
	at := slices.IndexFunc(st.decision.candidates, func(s tlpSelection) bool { return s.tag == "a-new" })
	if p1.gen <= p0.gen || at < 0 || st.decision.candidates[at].reason != reasonUnobserved {
		t.Fatalf("E20 add: gen %d->%d candidates %+v", p0.gen, p1.gen, st.decision.candidates)
	}
	if err := ohm.RemoveHandler(context.Background(), "f-backup"); err != nil {
		t.Fatal(err)
	}
	if _, st := f.pass(t, "t"); st.decision.fallbackOf == nil || st.decision.fallbackOf.reason != reasonAbsent {
		t.Fatalf("E20 remove: fallback %+v", st.decision.fallbackOf)
	}
}

// E21: a pass never modifies the statuses it reads.
func TestThresholdPassLeavesTheObservationAlone(t *testing.T) {
	f := startPassCore(t, basicConfig())
	f.obs.shared.Store(true)
	f.obs.set("a-hy2", alive(ms(60)))
	f.obs.set("f-backup", alive(ms(10)))
	f.obs.mu.Lock()
	before := map[string]*observatory.OutboundStatus{}
	for tag, s := range f.obs.statuses {
		before[tag] = proto.Clone(s).(*observatory.OutboundStatus)
	}
	f.obs.mu.Unlock()
	f.obs.set("a-vless", alive(ms(70)))
	f.obs.mu.Lock()
	defer f.obs.mu.Unlock()
	for tag, s := range before {
		if !proto.Equal(s, f.obs.statuses[tag]) {
			t.Fatalf("E21: %s changed: %v -> %v", tag, s, f.obs.statuses[tag])
		}
	}
}

// E15: a pass exists before the first route; after Close no callback runs.
func TestThresholdLifecycle(t *testing.T) {
	f := startPassCore(t, basicConfig())
	if p := (*f.r.balancers.Load())["t"].pass.Load(); p == nil {
		t.Fatal("E15: no pass after start")
	}
	if err := f.instance.Close(); err != nil {
		t.Fatal(err)
	}
	gen := f.r.tlpGen.Load()
	f.obs.set("a-hy2", alive(ms(10)))
	if f.r.tlpGen.Load() != gen {
		t.Fatal("E15: a callback ran after Close")
	}
}

// E09: notifications, overrides, reloads, picks and reads together under -race.
func TestThresholdConcurrency(t *testing.T) {
	f := startPassCore(t, basicConfig())
	var wg sync.WaitGroup
	stop := make(chan struct{})
	run := func(fn func(i int)) {
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
	run(func(i int) { f.obs.set("a-hy2", alive(ms(float64(30+i%40)))) })
	run(func(i int) { f.obs.set("a-vless", alive(ms(float64(45+i%10)))) })
	run(func(i int) { _ = f.r.SetOverrideTarget("t", []string{"", "a-vless"}[i%2]) })
	run(func(int) { _, _ = pickThresholdRoute(f.r) })
	run(func(int) { _, _ = f.r.GetMembers("t"); _, _ = f.r.GetStatus("t") })
	run(func(i int) {
		config := basicConfig()
		if i%2 == 0 {
			config.BalancingRule = append(config.BalancingRule, thresholdOver("u", []string{"b-"}, "t", ms(50), 0))
		}
		_ = f.r.ReloadRules(config, false)
	})
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
