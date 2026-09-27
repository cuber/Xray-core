package anytls_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

func TestAnyTLSOutboundObservedBalancers(t *testing.T) {
	var fastCode, slowCode atomic.Int32
	fastCode.Store(204)
	slowCode.Store(204)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Query().Get("ob") {
		case "fast/one":
			w.WriteHeader(int(fastCode.Load()))
		case "slow":
			timer := time.NewTimer(80 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				w.WriteHeader(int(slowCode.Load()))
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	const root = "http://unresolvable.invalid:8080"
	const fastURI = "/generate_204?ob=fast%2Fone&keepalive=1"
	const slowURI = "/generate_204?ob=slow"
	f, config := outboundFixture(t, "", func(config map[string]any) {
		config["burstObservatory"] = map[string]any{"pingGroups": []any{
			map[string]any{"subjectSelector": []string{"measured-fast"}, "pingConfig": map[string]any{
				"destination": root + "/wrong-default", "interval": "50ms", "timeout": "1s", "sampling": 20,
				"destinationsByPrefix": map[string]string{"measured-": root + "/wrong-prefix", "measured-fast": root + fastURI},
			}},
			map[string]any{"subjectSelector": []string{"measured-slow"}, "pingConfig": map[string]any{
				"destination": root + slowURI, "interval": "50ms", "timeout": "1s", "sampling": 20,
			}},
		}}
		for _, value := range config["outbounds"].([]any) {
			out := value.(map[string]any)
			if out["tag"] == "a" {
				out["settings"].(map[string]any)["redirect"] = server.Listener.Addr().String()
			}
		}
	})
	ctx := context.WithValue(context.Background(), core.XrayKey(1), f.instance)
	observer := f.instance.GetFeature(extension.ObservatoryType()).(*burst.Observer)
	// Stop automatic sampling before publishing the selected handlers. Subsequent
	// Check calls still use the real dispatcher and the registered observer.
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	tags := []string{"measured-fast", "measured-slow"}
	for _, tag := range tags {
		out := proto.Clone(config).(*core.OutboundHandlerConfig)
		out.Tag = tag
		if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: out}); err != nil {
			t.Fatal(err)
		}
	}
	report := func() map[string]*observatory.OutboundStatus {
		t.Helper()
		value, err := observer.GetObservation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]*observatory.OutboundStatus)
		for _, status := range value.(*observatory.ObservationResult).Status {
			result[status.OutboundTag] = status
		}
		return result
	}
	if len(report()) != 0 {
		t.Fatal("unmeasured handlers already have observations")
	}
	least := &router.LeastPingStrategy{}
	least.InjectContext(ctx)
	if got := least.PickOutbound(tags); got != "" {
		t.Fatal("unmeasured candidate selected", got)
	}
	for range 3 {
		observer.Check(tags)
	}
	initial := report()
	fast, slow := initial[tags[0]], initial[tags[1]]
	if fast == nil || slow == nil || !fast.Alive || !slow.Alive || fast.HealthPing.Average >= slow.HealthPing.Average {
		t.Fatal("controlled high RTT was not observed", initial)
	}
	if got := least.PickOutbound(tags); got != tags[0] {
		t.Fatal("leastPing did not select measured minimum", got)
	}
	rotation := router.NewWeightedLeastPingStrategy(&router.StrategyWeightedLeastPingConfig{
		Weights: []*router.StrategyWeight{{Match: tags[0], Value: 1.5}, {Match: tags[1], Value: 1}},
	})
	rotation.InjectContext(ctx)
	assertRatio := func() {
		t.Helper()
		counts := map[string]int{}
		for range 50 {
			counts[rotation.PickOutbound(tags)]++
		}
		if counts[tags[0]] != 30 || counts[tags[1]] != 20 {
			t.Fatal("literal 1.5:1 weights did not rotate 3:2", counts)
		}
	}
	assertRatio()
	// Derive the cutoff from measured RTTs, not a machine-speed assumption.
	weighted := router.NewWeightedLeastPingStrategy(&router.StrategyWeightedLeastPingConfig{
		MaxRTT: (fast.HealthPing.Average + slow.HealthPing.Average) / 2, MinSamples: 1,
	})
	weighted.InjectContext(ctx)
	if got := weighted.GetPrincipleTarget(tags); !reflect.DeepEqual(got, tags[:1]) {
		t.Fatal("high RTT must be alive but outside the weighted healthy pool", got)
	}
	slowCode.Store(503)
	observer.Check(tags[1:])
	afterFailure := report()
	if afterFailure[tags[1]].Alive || !proto.Equal(fast, afterFailure[tags[0]]) {
		t.Fatal("failed group changed the other group's samples", afterFailure)
	}
	slowCode.Store(204)
	for i := 1; i <= 3; i++ {
		observer.Check(tags[1:])
		if got := report()[tags[1]].Alive; got != (i == 3) {
			t.Fatalf("recovery sample %d alive=%v", i, got)
		}
	}
	tolerant := router.NewWeightedLeastPingStrategy(&router.StrategyWeightedLeastPingConfig{Tolerance: 0.1})
	tolerant.InjectContext(ctx)
	if got := tolerant.GetPrincipleTarget(tags); !reflect.DeepEqual(got, tags[:1]) {
		t.Fatal("recovered but excessive historical loss must remain outside tolerance", got)
	}
	fastCode.Store(503)
	slowCode.Store(503)
	observer.Check(tags)
	if got := least.PickOutbound(tags); got != "" {
		t.Fatal("leastPing selected dead candidate", got)
	}
	if got := weighted.GetPrincipleTarget(tags); !reflect.DeepEqual(got, tags) {
		t.Fatal("weighted all-unhealthy fallback must include all candidates", got)
	}
	assertRatio()
	counts := map[string]int{}
	for range 20 {
		counts[weighted.PickOutbound(tags)]++
	}
	if counts[tags[0]] != 10 || counts[tags[1]] != 10 {
		t.Fatal("equal-weight fallback did not rotate", counts)
	}
	// No scheduler runs here: let the real two-period validity expire, including
	// the observer's cached measurements. Empty history must not resurrect health.
	deadline := time.Now().Add(3 * time.Second)
	for {
		current := report()
		if current[tags[0]].HealthPing.All == 0 && current[tags[1]].HealthPing.All == 0 {
			if current[tags[0]].Alive || current[tags[1]].Alive || least.PickOutbound(tags) != "" {
				t.Fatal("stale observation remained healthy", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale samples did not expire", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, uri := range requests {
		if uri != fastURI && uri != slowURI {
			t.Fatal("probe URL override or escaping changed", uri)
		}
	}
	if len(requests) != 12 {
		t.Fatal("unexpected remote probe count", len(requests))
	}
}

func TestAnyTLSOutboundConnectivitySuppression(t *testing.T) {
	var targetCode, networkCode atomic.Int32
	var targetRequests, networkRequests atomic.Int32
	targetCode.Store(503)
	networkCode.Store(503)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(int(targetCode.Load()))
	}))
	t.Cleanup(target.Close)
	connectivity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkRequests.Add(1)
		w.WriteHeader(int(networkCode.Load()))
	}))
	t.Cleanup(connectivity.Close)
	f, _ := outboundFixture(t, "", func(config map[string]any) {
		for _, value := range config["outbounds"].([]any) {
			out := value.(map[string]any)
			if out["tag"] == "a" {
				out["settings"].(map[string]any)["redirect"] = target.Listener.Addr().String()
			}
		}
	})
	ctx := context.WithValue(context.Background(), core.XrayKey(1), f.instance)
	ping := burst.NewHealthPing(ctx, f.instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher), &burst.HealthPingConfig{
		Destination: "http://unresolvable.invalid:8080/generate_204", Connectivity: connectivity.URL,
		Timeout: int64(time.Second), SamplingCount: 1,
	})
	check := func() {
		t.Helper()
		if err := ping.Check([]string{"client"}); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if len(ping.Results) != 0 {
		t.Fatal("network-down suppression published a sample")
	}
	networkCode.Store(204)
	check()
	if result := ping.Results["client"].Get(); result.Alive || result.Fail != 1 || result.All != 1 {
		t.Fatal("reachable network with failed remote endpoint must be dead", result)
	}
	targetCode.Store(204)
	check()
	if result := ping.Results["client"].Get(); !result.Alive || result.Fail != 0 || result.All != 1 {
		t.Fatal("healthy endpoint did not recover", result)
	}
	if targetRequests.Load() != 3 || networkRequests.Load() != 2 {
		t.Fatalf("target/network request counts=%d/%d", targetRequests.Load(), networkRequests.Load())
	}
}

func TestAnyTLSOutboundObserverStopRestart(t *testing.T) {
	var blocked atomic.Bool
	blocked.Store(true)
	entered := make(chan struct{}, 64)
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		entered <- struct{}{}
		if blocked.Load() {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(server.Close)
	f, _ := outboundFixture(t, "", func(config map[string]any) {
		config["burstObservatory"] = map[string]any{
			"subjectSelector": []string{"client"},
			"pingConfig":      map[string]any{"destination": "http://unresolvable.invalid:8080/generate_204", "interval": "3s", "timeout": "2s", "sampling": 20},
		}
		for _, value := range config["outbounds"].([]any) {
			out := value.(map[string]any)
			if out["tag"] == "a" {
				out["settings"].(map[string]any)["redirect"] = server.Listener.Addr().String()
			}
		}
	})
	observer := f.instance.GetFeature(extension.ObservatoryType()).(*burst.Observer)
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	// An initial scheduler run can race with outboundFixture publishing client.
	// Drain that generation before testing a deliberately started generation.
	waitIdle := func() {
		t.Helper()
		// Remote FIN preserves the configured one-second downlink-only window;
		// local cancellation must not require destroying the shared session.
		deadline := time.Now().Add(3 * time.Second)
		for active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if active.Load() != 0 {
			t.Fatal("remote HTTP request survived the policy drain window")
		}
	}
	waitIdle()
	for len(entered) > 0 {
		<-entered
	}
	if err := observer.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled request did not reach remote HTTP server")
	}
	started := time.Now()
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("observer stop waited for HTTP timeout instead of canceling")
	}
	waitIdle()
	result, err := observer.GetObservation(context.Background())
	if err != nil || len(result.(*observatory.ObservationResult).Status) != 0 {
		t.Fatal("canceled probe published a late sample", result, err)
	}
	blocked.Store(false)
	if err := observer.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		result, err := observer.GetObservation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		statuses := result.(*observatory.ObservationResult).Status
		if len(statuses) == 1 && statuses[0].Alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted observer did not recover", result)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}
