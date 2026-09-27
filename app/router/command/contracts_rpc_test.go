package command

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/geodata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func contractRPCRule(tag string) *router.RoutingRule {
	return &router.RoutingRule{
		RuleTag: tag, TargetTag: &router.RoutingRule_Tag{Tag: "out-" + tag},
		UserEmail: []string{"domain:" + tag}, InboundTag: []string{"entry-" + tag}, Networks: []xnet.Network{xnet.Network_TCP},
		Domain: []*geodata.DomainRule{{Value: &geodata.DomainRule_Custom{Custom: &geodata.Domain{Type: geodata.Domain_Domain, Value: tag + ".example"}}}},
		Ip:     []*geodata.IPRule{{Value: &geodata.IPRule_Custom{Custom: &geodata.CIDRRule{Cidr: &geodata.CIDR{Ip: []byte{10, 0, 0, 0}, Prefix: 8}}}}},
	}
}

func contractRPCClient(t *testing.T, rules []*router.RoutingRule) (RoutingServiceClient, *router.Router) {
	t.Helper()
	r := new(router.Router)
	if err := r.Init(context.Background(), &router.Config{Rule: rules}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterRoutingServiceServer(server, NewRoutingServer(r, nil))
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); <-done; _ = r.Close() })
	return NewRoutingServiceClient(conn), r
}

func contractAssertRules(t *testing.T, response *ListRuleResponse, expected ...*router.RoutingRule) {
	t.Helper()
	if len(response.Rules) != len(expected) {
		t.Fatalf("rules=%v want %d", response, len(expected))
	}
	for i, want := range expected {
		got := response.Rules[i]
		if got.Tag != want.GetTag() || got.RuleTag != want.RuleTag || !proto.Equal(got.Rule, want) {
			t.Fatalf("rule[%d]=%v want %v", i, got, want)
		}
	}
}

func TestContractRealGRPCRuleLifecycle(t *testing.T) {
	a, b, c := contractRPCRule("a"), contractRPCRule("b"), contractRPCRule("c")
	client, r := contractRPCClient(t, []*router.RoutingRule{a, b})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list := func() *ListRuleResponse {
		response, err := client.ListRule(ctx, &ListRuleRequest{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("ListRule: %v", response)
		return response
	}
	contractAssertRules(t, list(), a, b)
	add := func(appendRules bool, rules ...*router.RoutingRule) error {
		_, err := client.AddRule(ctx, &AddRuleRequest{Config: serial.ToTypedMessage(&router.Config{Rule: rules}), ShouldAppend: appendRules})
		return err
	}
	if err := add(true, c); err != nil {
		t.Fatal(err)
	}
	contractAssertRules(t, list(), a, b, c)
	if _, err := client.RemoveRule(ctx, &RemoveRuleRequest{RuleTag: "b"}); err != nil {
		t.Fatal(err)
	}
	contractAssertRules(t, list(), a, c)
	if err := add(true, a); err == nil {
		t.Fatal("duplicate append accepted")
	}
	contractAssertRules(t, list(), a, c)
	replacement := contractRPCRule("a")
	replacement.UserEmail = []string{"replacement@a"}
	if err := add(false, replacement); err != nil {
		t.Fatal(err)
	}
	response := list()
	contractAssertRules(t, response, replacement)
	// Mutate nested wire response and a direct service response. The latter
	// proves clone isolation rather than relying solely on protobuf transport.
	mutate := func(resp *ListRuleResponse) {
		resp.Rules[0].Rule.UserEmail[0] = "mutated"
		resp.Rules[0].Rule.Domain[0].GetCustom().Value = "mutated"
		resp.Rules[0].Rule.Ip[0].GetCustom().Cidr.Ip[0] = 99
		resp.Rules[0].Rule.TargetTag = &router.RoutingRule_Tag{Tag: "mutated"}
	}
	mutate(response)
	direct, err := NewRoutingServer(r, nil).ListRule(ctx, &ListRuleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	mutate(direct)
	contractAssertRules(t, list(), replacement)
	if _, err := client.RemoveRule(ctx, &RemoveRuleRequest{RuleTag: ""}); err == nil {
		t.Fatal("empty removal accepted")
	}
	if _, err := client.RemoveRule(ctx, &RemoveRuleRequest{RuleTag: "absent"}); err != nil {
		t.Fatalf("absent removal not idempotent: %v", err)
	}
	contractAssertRules(t, list(), replacement)
	if _, err := client.RemoveRule(ctx, &RemoveRuleRequest{RuleTag: "a"}); err != nil {
		t.Fatal(err)
	}
	contractAssertRules(t, list())
}

func TestContractRealGRPCConcurrentRules(t *testing.T) {
	base := contractRPCRule("base")
	client, _ := contractRPCClient(t, []*router.RoutingRule{base})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range 100 {
				tag := fmt.Sprintf("w%d-%d", worker, i)
				rule := contractRPCRule(tag)
				if _, err := client.AddRule(ctx, &AddRuleRequest{Config: serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{rule}}), ShouldAppend: true}); err != nil {
					t.Error(err)
					return
				}
				response, err := client.ListRule(ctx, &ListRuleRequest{})
				if err != nil {
					t.Error(err)
					return
				}
				found := false
				seen := map[string]bool{}
				for index, item := range response.Rules {
					if seen[item.RuleTag] {
						t.Errorf("duplicate rule: %s", item.RuleTag)
					}
					seen[item.RuleTag] = true
					if index == 0 && !proto.Equal(item.Rule, base) {
						t.Error("static order/content changed")
					}
					if item.RuleTag == tag {
						found = true
						if !proto.Equal(item.Rule, rule) {
							t.Errorf("own rule changed: %v", item)
						}
					}
					if item.Rule == nil || item.RuleTag != item.Rule.RuleTag || item.Tag != item.Rule.GetTag() {
						t.Errorf("inconsistent snapshot: %v", item)
					}
					item.Rule.UserEmail[0] = "mutated response"
				}
				if !found {
					t.Errorf("added rule %s missing", tag)
				}
				if _, err := client.RemoveRule(ctx, &RemoveRuleRequest{RuleTag: tag}); err != nil {
					t.Error(err)
					return
				}
				response, err = client.ListRule(ctx, &ListRuleRequest{})
				if err != nil {
					t.Error(err)
					return
				}
				for _, item := range response.Rules {
					if item.RuleTag == tag {
						t.Errorf("removed rule %s remains", tag)
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	response, err := client.ListRule(ctx, &ListRuleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	contractAssertRules(t, response, base)
}

func TestContractListRuleLegacyWireFields(t *testing.T) {
	rule := contractRPCRule("legacy")
	client, _ := contractRPCClient(t, []*router.RoutingRule{rule})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.ListRule(ctx, &ListRuleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(response.Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	// An old decoder knows fields 1/2 and skips the new detailed-rule field 3.
	values := map[protowire.Number]string{}
	for len(data) > 0 {
		number, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			t.Fatal("bad tag")
		}
		data = data[n:]
		if number == 1 || number == 2 {
			if typ != protowire.BytesType {
				t.Fatal("legacy wire type changed")
			}
			value, n := protowire.ConsumeString(data)
			if n < 0 {
				t.Fatal("bad string")
			}
			values[number] = value
			data = data[n:]
		} else {
			n := protowire.ConsumeFieldValue(number, typ, data)
			if n < 0 {
				t.Fatal("bad unknown field")
			}
			data = data[n:]
		}
	}
	if values[1] != "out-legacy" || values[2] != "legacy" {
		t.Fatalf("legacy fields=%v", values)
	}
}

func TestContractRealGRPCConcurrentReplacement(t *testing.T) {
	a, b := contractRPCRule("a"), contractRPCRule("b")
	client, _ := contractRPCClient(t, []*router.RoutingRule{a, b})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := range 100 {
			rules := []*router.RoutingRule{a, b}
			if i%2 == 0 {
				rules = []*router.RoutingRule{b, a}
			}
			_, err := client.AddRule(ctx, &AddRuleRequest{Config: serial.ToTypedMessage(&router.Config{Rule: rules}), ShouldAppend: false})
			if err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				response, err := client.ListRule(ctx, &ListRuleRequest{})
				if err != nil {
					t.Error(err)
					return
				}
				if len(response.Rules) != 2 {
					t.Errorf("partial replacement: %v", response)
					return
				}
				first, second := response.Rules[0].Rule, response.Rules[1].Rule
				if !(proto.Equal(first, a) && proto.Equal(second, b)) && !(proto.Equal(first, b) && proto.Equal(second, a)) {
					t.Errorf("mixed replacement: %v", response)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
