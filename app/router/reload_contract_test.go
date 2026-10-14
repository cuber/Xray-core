package router

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	routing_session "github.com/xtls/xray-core/features/routing/session"
)

func reloadTestRule(tag string) *RoutingRule {
	return &RoutingRule{RuleTag: tag, InboundTag: []string{"entry"}, TargetTag: &RoutingRule_Tag{Tag: tag}}
}

func TestReloadFailureClosesOnlyUnpublishedWebhooks(t *testing.T) {
	for _, appendRules := range []bool{false, true} {
		for _, failure := range []string{"condition", "duplicate", "balancer", "notifier"} {
			t.Run(fmt.Sprintf("append=%v/%s", appendRules, failure), func(t *testing.T) {
				r := new(Router)
				old := reloadTestRule("old")
				old.Webhook = &WebhookConfig{Url: "http://127.0.0.1/old"}
				if err := r.Init(context.Background(), &Config{Rule: []*RoutingRule{old}}, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { r.Close() })
				oldRules, oldBalancers := r.rules.Load(), r.balancers.Load()
				first := reloadTestRule("first")
				first.Webhook = &WebhookConfig{Url: "http://127.0.0.1/new"}
				second := reloadTestRule("second")
				switch failure {
				case "condition":
					second = &RoutingRule{}
				case "duplicate":
					second.RuleTag = first.RuleTag
				case "balancer":
					second.Webhook = first.Webhook
					second.TargetTag = &RoutingRule_BalancingTag{BalancingTag: "missing"}
				case "notifier":
					second.Webhook = &WebhookConfig{Url: "fail"}
				}
				var created []*WebhookNotifier
				factory := func(cfg *WebhookConfig) (*WebhookNotifier, error) {
					if cfg.Url == "fail" {
						return nil, fmt.Errorf("injected constructor failure")
					}
					h, err := NewWebhookNotifier(cfg)
					if h != nil {
						created = append(created, h)
						t.Cleanup(func() { h.Close() })
					}
					return h, err
				}
				if err := r.reloadRules(&Config{Rule: []*RoutingRule{first, second}}, appendRules, factory); err == nil {
					t.Fatal("invalid reload succeeded")
				}
				if len(created) == 0 {
					t.Fatal("test did not allocate a webhook")
				}
				for _, h := range created {
					select {
					case <-h.done:
					default:
						t.Error("unpublished webhook was not closed")
					}
				}
				if r.rules.Load() != oldRules || r.balancers.Load() != oldBalancers {
					t.Fatal("failed reload published state")
				}
				select {
				case <-(*oldRules)[0].Webhook.done:
					t.Fatal("failed reload closed existing webhook")
				default:
				}
				// Inspect selection without firing the live webhook.
				picked, _, err := r.pickRouteInternal(&routing_session.Context{Inbound: &session.Inbound{Tag: "entry"}})
				if err != nil || picked.RuleTag != "old" {
					t.Fatalf("old rule unusable: %v, %v", picked, err)
				}
			})
		}
	}
}

func TestReloadConcurrentSelectionAndMetadata(t *testing.T) {
	r := new(Router)
	if err := r.Init(context.Background(), &Config{Rule: []*RoutingRule{reloadTestRule("a")}}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	ctx := &routing_session.Context{Inbound: &session.Inbound{Tag: "entry"}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 300 {
				picked, err := r.PickRoute(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				route := picked.(*Route)
				if route.GetRule().GetTag() != route.GetOutboundTag() || route.GetRule().GetRuleTag() != route.GetRuleTag() {
					t.Error("mixed selection snapshot")
					return
				}
				list := r.ListRule()
				if len(list) != 1 {
					t.Error("partial list snapshot")
					return
				}
				detail := list[0].(*Route).GetRule()
				if detail.GetTag() != list[0].GetOutboundTag() {
					t.Error("mixed list metadata")
					return
				}
				detail.InboundTag[0] = "mutated"
			}
		}()
	}
	for i := range 300 {
		rule := reloadTestRule([]string{"a", "b"}[i%2])
		if err := r.ReloadRules(&Config{Rule: []*RoutingRule{rule}}, false); err != nil {
			t.Fatal(err)
		}
		rule.InboundTag[0] = "caller-mutated"
		rule.TargetTag.(*RoutingRule_Tag).Tag = "caller-mutated"
	}
	wg.Wait()
}

func TestReloadValidatesBeforeBalancerFeatureRegistration(t *testing.T) {
	r := new(Router)
	if err := r.Init(context.Background(), &Config{}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	// This context deliberately has no Core instance. Validation must reject
	// the rule before an observer-dependent strategy registers with Core.
	err := r.ReloadRules(&Config{
		BalancingRule: []*BalancingRule{{Tag: "weighted", Strategy: "weightedleastping", StrategySettings: serial.ToTypedMessage(&StrategyWeightedLeastPingConfig{})}},
		Rule:          []*RoutingRule{{}},
	}, false)
	if err == nil || len(r.ListRule()) != 0 || len(*r.balancers.Load()) != 0 {
		t.Fatalf("invalid reload altered state: %v", err)
	}
}

func TestReloadPublishedWebhookOwnership(t *testing.T) {
	r := new(Router)
	if err := r.Init(context.Background(), &Config{}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	var previous *WebhookNotifier
	for i, appendRules := range []bool{false, true, false} {
		rule := reloadTestRule(fmt.Sprint(i))
		rule.Webhook = &WebhookConfig{Url: "http://127.0.0.1/unused"}
		if err := r.ReloadRules(&Config{Rule: []*RoutingRule{rule}}, appendRules); err != nil {
			t.Fatal(err)
		}
		if previous != nil {
			closed := false
			select {
			case <-previous.done:
				closed = true
			default:
			}
			if closed == appendRules {
				t.Fatalf("append=%v previous closed=%v", appendRules, closed)
			}
		}
		rules := *r.rules.Load()
		previous = rules[len(rules)-1].Webhook
		select {
		case <-previous.done:
			t.Fatal("published webhook was closed")
		default:
		}
	}
	if err := r.RemoveRule("2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-previous.done:
	default:
		t.Fatal("removed webhook remains open")
	}
}
