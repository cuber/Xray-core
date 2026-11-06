package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/xtls/xray-core/app/proxyman"
	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/app/router"
	. "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/loopback"
	"github.com/xtls/xray-core/transport/internet"
)

// Core spec018 S3.2-S3.4 (FR-005, FR-006): changes through the RPCs that would create a loop are
// refused and change nothing; accepted ones apply.

type loopFixture struct {
	routing  RoutingServiceClient
	handlers handlercmd.HandlerServiceClient
}

// newLoopFixture starts a Core whose default outbound is the loopback lo, feeding inbound via. A
// connection lo feeds back takes balancer b only for x.test, then via-out to direct, so nothing
// loops until a change removes via-out or points b at lo.
func newLoopFixture(t *testing.T) *loopFixture {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	raw, err := json.Marshal(map[string]any{
		"api": map[string]any{
			"tag": "api", "listen": fmt.Sprintf("127.0.0.1:%d", apiPort),
			"services": []string{"RoutingService", "HandlerService"},
		},
		"outbounds": []any{
			map[string]any{"tag": "lo", "protocol": "loopback", "settings": map[string]any{"inboundTag": "via"}},
			map[string]any{"tag": "direct", "protocol": "freedom"},
		},
		"routing": map[string]any{
			"balancers": []any{map[string]any{"tag": "b", "selector": []string{"direct"}, "fallbackTag": "direct"}},
			"rules": []any{
				map[string]any{
					"type": "field", "inboundTag": []string{"via"}, "domain": []string{"full:x.test"},
					"balancerTag": "b", "ruleTag": "via-b",
				},
				map[string]any{"type": "field", "inboundTag": []string{"via"}, "outboundTag": "direct", "ruleTag": "via-out"},
			},
		},
		// A balancer with a fallbackTag observes its candidates.
		"observatory": map[string]any{"subjectSelector": []string{"direct"}, "probeUrl": "http://127.0.0.1:9/", "probeInterval": "1h"},
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
	return &loopFixture{routing: NewRoutingServiceClient(conn), handlers: handlercmd.NewHandlerServiceClient(conn)}
}

func call(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func refused(t *testing.T, what string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: %v, want %q", what, err, want)
	}
}

func (f *loopFixture) ruleTags(t *testing.T) string {
	t.Helper()
	list, err := f.routing.ListRule(call(t), &ListRuleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, rule := range list.Rules {
		tags = append(tags, rule.RuleTag)
	}
	return strings.Join(tags, ",")
}

func (f *loopFixture) outboundTags(t *testing.T) string {
	t.Helper()
	list, err := f.handlers.ListOutbounds(call(t), &handlercmd.ListOutboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, ob := range list.Outbounds {
		tags = append(tags, ob.Tag)
	}
	return strings.Join(tags, ",")
}

func ruleTo(ruleTag, inbound, outboundTag string) *serial.TypedMessage {
	return serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{{
		RuleTag: ruleTag, InboundTag: []string{inbound}, TargetTag: &router.RoutingRule_Tag{Tag: outboundTag},
	}}})
}

func loopbackTo(tag, inbound string) *handlercmd.AddOutboundRequest {
	return &handlercmd.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{
		Tag: tag, ProxySettings: serial.ToTypedMessage(&loopback.Config{InboundTag: inbound}),
	}}
}

func TestLoopChecksOnChange(t *testing.T) {
	f := newLoopFixture(t)

	_, err := f.routing.RemoveRule(call(t), &RemoveRuleRequest{RuleTag: "via-out"})
	refused(t, "RemoveRule via-out", err, "loopback loop: via -> [default] lo -> via")
	_, err = f.routing.AddRule(call(t), &AddRuleRequest{Config: ruleTo("x", "via", "lo")})
	refused(t, "AddRule replacing with via -> lo", err, "loopback loop: via -> [rule x] lo -> via")
	if got := f.ruleTags(t); got != "via-b,via-out" {
		t.Fatalf("rules after refusals = %s", got)
	}

	_, err = f.routing.OverrideBalancerTarget(call(t), &OverrideBalancerTargetRequest{BalancerTag: "b", Target: "lo"})
	refused(t, "override b to lo", err, "loopback loop: via -> [rule via-b, balancer b override] lo -> via")
	info, err := f.routing.GetBalancerInfo(call(t), &GetBalancerInfoRequest{Tag: "b"})
	if err != nil || info.Balancer.Override.GetTarget() != "" {
		t.Fatalf("b's override after the refusal = %v, %v", info, err)
	}
	if _, err := f.routing.OverrideBalancerTarget(call(t), &OverrideBalancerTargetRequest{BalancerTag: "b", Target: "direct"}); err != nil {
		t.Fatalf("override b to direct: %v", err)
	}

	_, err = f.handlers.AddOutbound(call(t), &handlercmd.AddOutboundRequest{Outbound: &core.OutboundHandlerConfig{
		Tag: "self",
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{StreamSettings: &internet.StreamConfig{
			SocketSettings: &internet.SocketConfig{DialerProxy: "self"},
		}}),
		ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
	}})
	refused(t, "AddOutbound dialing through itself", err, "dialerProxy cycle: self -> self")

	// A rule to a loopback that does not exist yet is no loop; the loopback it names would close one.
	if _, err := f.routing.AddRule(call(t), &AddRuleRequest{Config: ruleTo("to-lo2", "via2", "lo2"), ShouldAppend: true}); err != nil {
		t.Fatalf("AddRule via2 -> lo2: %v", err)
	}
	_, err = f.handlers.AddOutbound(call(t), loopbackTo("lo2", "via2"))
	refused(t, "AddOutbound lo2", err, "loopback loop: via2 -> [rule to-lo2] lo2 -> via2")
	if _, err := f.handlers.AddOutbound(call(t), loopbackTo("lo3", "via")); err != nil {
		t.Fatalf("AddOutbound lo3 into via, which leads to direct: %v", err)
	}
	if got := f.ruleTags(t); got != "via-b,via-out,to-lo2" {
		t.Fatalf("rules = %s", got)
	}
	for _, tag := range []string{"self", "lo2"} {
		if strings.Contains(","+f.outboundTags(t)+",", ","+tag+",") {
			t.Fatalf("refused outbound %s was added: %s", tag, f.outboundTags(t))
		}
	}
}

// FR-006 (S3.4): a rule to a loopback and that loopback, each harmless alone, close a loop together;
// sent at once, at most one is accepted.
func TestLoopChecksAreSerialised(t *testing.T) {
	f := newLoopFixture(t)
	for i := range 20 {
		inbound, tag := fmt.Sprint("via-c", i), fmt.Sprint("lo-c", i)
		var wg sync.WaitGroup
		var ruleErr, outboundErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, ruleErr = f.routing.AddRule(call(t), &AddRuleRequest{Config: ruleTo("r"+tag, inbound, tag), ShouldAppend: true})
		}()
		go func() {
			defer wg.Done()
			_, outboundErr = f.handlers.AddOutbound(call(t), loopbackTo(tag, inbound))
		}()
		wg.Wait()
		if ruleErr == nil && outboundErr == nil {
			t.Fatalf("round %d: both the rule and the loopback were accepted", i)
		}
		if ruleErr != nil && outboundErr != nil {
			t.Fatalf("round %d: both refused: %v; %v", i, ruleErr, outboundErr)
		}
	}
}
