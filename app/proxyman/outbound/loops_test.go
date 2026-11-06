package outbound

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

// Core spec018 S1 (FR-001) and S2 without a routing section.

// loopHandler is a handler that the loop checks see as dialing through via, or as a loopback.
type loopHandler struct {
	tag, via, loopback string
	isLoopback         bool
}

func (h *loopHandler) Start() error                              { return nil }
func (h *loopHandler) Close() error                              { return nil }
func (h *loopHandler) Tag() string                               { return h.tag }
func (h *loopHandler) Dispatch(context.Context, *transport.Link) {}
func (h *loopHandler) SenderSettings() *serial.TypedMessage      { return nil }
func (h *loopHandler) ProxySettings() *serial.TypedMessage       { return nil }
func (h *loopHandler) DialerProxy() string                       { return h.via }
func (h *loopHandler) LoopbackInbound() (string, bool)           { return h.loopback, h.isLoopback }
func dials(tag, via string) outbound.Handler                     { return &loopHandler{tag: tag, via: via} }

func loops(tag, inbound string) outbound.Handler {
	return &loopHandler{tag: tag, loopback: inbound, isLoopback: true}
}

func TestDialerProxyCycles(t *testing.T) {
	refused := []struct {
		name     string
		handlers []outbound.Handler
		want     string
	}{
		{"self", []outbound.Handler{dials("a", "a")}, "dialerProxy cycle: a -> a"},
		{"two", []outbound.Handler{dials("b", "a"), dials("a", "b")}, "dialerProxy cycle: a -> b -> a"},
		{"three", []outbound.Handler{dials("c", "a"), dials("a", "b"), dials("b", "c")}, "dialerProxy cycle: a -> b -> c -> a"},
		{"a tail into a cycle", []outbound.Handler{dials("t", "x"), dials("x", "y"), dials("y", "x")}, "dialerProxy cycle: x -> y -> x"},
	}
	for _, c := range refused {
		if err := checkDialerProxyCycles(c.handlers); err == nil || !strings.HasSuffix(err.Error(), ": "+c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	var chain []outbound.Handler
	for i := range 32 {
		chain = append(chain, dials(fmt.Sprint("h", i), fmt.Sprint("h", i+1)))
	}
	accepted := map[string][]outbound.Handler{
		"a 32-handler chain":         chain,
		"a dialerProxy naming none":  {dials("a", "gone")},
		"handlers without the view":  {&struct{ outbound.Handler }{dials("x", "x")}},
		"an untagged handler's edge": {dials("", "a"), dials("a", "")},
	}
	for name, handlers := range accepted {
		if err := checkDialerProxyCycles(handlers); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDefaultLoopWithoutRouting(t *testing.T) {
	lo := loops("lo", "via")
	out := dials("out", "lo")
	if err := checkDefaultLoop([]outbound.Handler{lo}, lo); err == nil ||
		!strings.HasSuffix(err.Error(), ": loopback loop: via -> [default] lo -> via") {
		t.Fatalf("a loopback default: %v", err)
	}
	if err := checkDefaultLoop([]outbound.Handler{out, lo}, out); err == nil ||
		!strings.Contains(err.Error(), "[default] out via lo -> via") {
		t.Fatalf("a default dialing through a loopback: %v", err)
	}
	direct := dials("direct", "")
	if err := checkDefaultLoop([]outbound.Handler{direct, lo}, direct); err != nil {
		t.Fatalf("a direct default: %v", err)
	}
	if err := checkDefaultLoop(nil, nil); err != nil {
		t.Fatalf("no default: %v", err)
	}
}
