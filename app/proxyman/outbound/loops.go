package outbound

import (
	"context"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/loopcheck"
	"github.com/xtls/xray-core/features/outbound"
)

// Core spec018: the manager refuses a dialerProxy cycle (S1) and asks the router about loops
// through loopbacks (S2), at start and when a handler is added, one check at a time (S3).

// checkDialerProxyCycles refuses handlers whose dialerProxy graph has a cycle, a self-reference
// included (S1). Each handler dials through at most one other; a tag naming no handler ends a walk.
func checkDialerProxyCycles(handlers []outbound.Handler) error {
	next := make(map[string]string, len(handlers))
	for _, h := range handlers {
		if view, ok := h.(outbound.LoopView); ok && h.Tag() != "" {
			if via := view.DialerProxy(); via != "" {
				next[h.Tag()] = via
			}
		}
	}
	if cycles := loopcheck.Functional(next); len(cycles) > 0 {
		return errors.New("dialerProxy cycle: ", strings.Join(cycles, "; "))
	}
	return nil
}

// checkDefaultLoop is S2 without a router of its own (no routing section): every connection a
// loopback feeds back goes to the default outbound, so a default that reaches a loopback loops.
func checkDefaultLoop(handlers []outbound.Handler, def outbound.Handler) error {
	if def == nil {
		return nil
	}
	byTag := make(map[string]outbound.Handler, len(handlers))
	for _, h := range handlers {
		if h.Tag() != "" {
			byTag[h.Tag()] = h
		}
	}
	inbound, path, ok := outbound.LoopbackReached(def, byTag)
	if !ok {
		return nil
	}
	return errors.New("loopback loop: ", inbound, " -> [default] ", path, " -> ", inbound)
}

// checkLoops runs S1, then S2 through the router, over handlers with def as the default outbound.
func (m *Manager) checkLoops(handlers []outbound.Handler, def outbound.Handler) error {
	if err := checkDialerProxyCycles(handlers); err != nil {
		return err
	}
	m.access.RLock()
	checker := m.loopChecker
	m.access.RUnlock()
	if checker != nil {
		return checker.CheckLoopbackLoops(handlers, def)
	}
	return checkDefaultLoop(handlers, def)
}

// CheckLoops implements outbound.GraphGuard: the start check over every handler (S3.1).
func (m *Manager) CheckLoops() error {
	defer m.LockGraph()()
	return m.checkLoops(m.ListHandlers(context.Background()), m.GetDefaultHandler())
}

// LockGraph implements outbound.GraphGuard (S3.4).
func (m *Manager) LockGraph() func() {
	m.graph.Lock()
	return m.graph.Unlock
}

// SetLoopChecker implements outbound.GraphGuard.
func (m *Manager) SetLoopChecker(checker outbound.LoopChecker) {
	m.access.Lock()
	defer m.access.Unlock()
	m.loopChecker = checker
}
