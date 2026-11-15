package command_test

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
)

type thresholdFixture struct {
	routing  RoutingServiceClient
	handlers handlercmd.HandlerServiceClient
}

// newThresholdFixture starts a Core whose observatory probes a- and f- outbounds (to a closed local
// port) and whose balancer t is thresholdLeastPing over a- with fallback f-backup. Its selector is
// "a", so an outbound named ax-* matches t without being observed.
func newThresholdFixture(t *testing.T) *thresholdFixture {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	outbounds := []any{map[string]any{"tag": "block", "protocol": "blackhole"}}
	for _, tag := range []string{"direct", "a-hy2", "a-vless", "f-backup"} {
		outbounds = append(outbounds, map[string]any{"tag": tag, "protocol": "freedom"})
	}
	raw, err := json.Marshal(map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"RoutingService", "HandlerService"},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"balancers": []any{map[string]any{
				"tag": "t", "selector": []string{"a"}, "fallbackTag": "f-backup",
				"strategy": map[string]any{"type": "thresholdLeastPing", "settings": map[string]any{"maxRTT": "50ms", "recoverRTT": "40ms"}},
			}},
			"rules": []any{map[string]any{"type": "field", "domain": []string{"full:t.test"}, "balancerTag": "t"}},
		},
		"observatory": map[string]any{"subjectSelector": []string{"a-", "f-"}, "probeUrl": "http://127.0.0.1:9/", "probeInterval": "1h"},
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
	return &thresholdFixture{routing: NewRoutingServiceClient(conn), handlers: handlercmd.NewHandlerServiceClient(conn)}
}

func (f *thresholdFixture) balancers(t *testing.T) string {
	t.Helper()
	list, err := f.routing.ListBalancers(call(t), &ListBalancersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range list.Balancers {
		out = append(out, b.Tag+":"+b.Balancer.Strategy)
	}
	return strings.Join(out, ",")
}

func thresholdBalancer(tag, fallback string, maxRTT time.Duration) *router.BalancingRule {
	return &router.BalancingRule{
		Tag: tag, OutboundSelector: []string{"a-"}, Strategy: "thresholdleastping", FallbackTag: fallback,
		StrategySettings: serial.ToTypedMessage(&router.StrategyThresholdLeastPingConfig{MaxRTT: int64(maxRTT)}),
	}
}

// Core spec027 C09, O06, O08 over the real RPCs: the strategy reports its name (the capability
// signal); AddRule publishes a valid thresholdLeastPing balancer and refuses an invalid or unobserved
// one without changing the map; AddOutbound refuses an unobserved outbound a thresholdLeastPing
// selector would match; RemoveOutbound of its fallback is not checked (D15).
func TestThresholdLeastPingConfigOverRPC(t *testing.T) {
	f := newThresholdFixture(t)
	if got := f.balancers(t); got != "t:thresholdleastping" {
		t.Fatalf("balancers at start = %s", got)
	}
	add := func(rule *router.BalancingRule) error {
		_, err := f.routing.AddRule(call(t), &AddRuleRequest{ShouldAppend: true, Config: serial.ToTypedMessage(&router.Config{
			BalancingRule: []*router.BalancingRule{rule},
		})})
		return err
	}
	if err := add(thresholdBalancer("t2", "block", 50*time.Millisecond)); err != nil {
		t.Fatalf("AddRule t2: %v", err)
	}
	refused(t, "AddRule zero maxRTT", add(thresholdBalancer("t3", "block", 0)), "maxRTT must be positive")
	refused(t, "AddRule unobserved fallback", add(thresholdBalancer("t3", "direct", 50*time.Millisecond)),
		"balancer t3: outbound direct is not observed (thresholdLeastPing needs every outbound it can choose observed)")
	refused(t, "AddRule fallback naming nothing", add(thresholdBalancer("t3", "nothing", 50*time.Millisecond)),
		"balancer t3: fallbackTag nothing names no outbound and no balancer")
	if got := f.balancers(t); got != "t:thresholdleastping,t2:thresholdleastping" {
		t.Fatalf("balancers after the refusals = %s", got)
	}

	_, err := f.handlers.AddOutbound(call(t), &handlercmd.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{
		Tag: "ax-new", ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
	}})
	refused(t, "AddOutbound ax-new", err, "balancer t: outbound ax-new is not observed")
	_, err = f.handlers.AddOutbound(call(t), &handlercmd.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{
		Tag: "a-new", ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
	}})
	if err != nil {
		t.Fatalf("AddOutbound a-new, observed: %v", err)
	}
	if _, err := f.handlers.RemoveOutbound(call(t), &handlercmd.RemoveOutboundRequest{Tag: "f-backup"}); err != nil {
		t.Fatalf("RemoveOutbound f-backup: %v", err)
	}
}
