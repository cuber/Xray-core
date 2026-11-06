package outbound

import "strings"

// Core spec018: loops one Core can decide from its own configuration are refused before they run.

// LoopView is what the loop checks read of a handler. A handler without it (one an app registers,
// such as the API's) dials through no other outbound and is no loopback.
type LoopView interface {
	// DialerProxy is the tag of the outbound this handler dials through (sockopt.dialerProxy), ""
	// for none (S1).
	DialerProxy() string
	// LoopbackInbound is the inbound tag through which the handler feeds connections back into
	// routing; ok is false for a handler that is no loopback (S2).
	LoopbackInbound() (tag string, ok bool)
}

// LoopChecker is implemented by a router that decides whether routing can bring a connection a
// loopback fed back into routing to a loopback again (S2), over handlers with def as the default
// outbound (nil for none).
type LoopChecker interface {
	CheckLoopbackLoops(handlers []Handler, def Handler) error
}

// GraphGuard is implemented by an outbound manager that refuses loops before they run (S3).
type GraphGuard interface {
	// CheckLoops checks every handler (S1) and, through the router, routing (S2): the start check.
	CheckLoops() error
	// LockGraph serialises one change of the routing or handler graph with its check (S3.4).
	LockGraph() (unlock func())
	// SetLoopChecker registers the router's S2 check.
	SetLoopChecker(LoopChecker)
}

// LoopbackReached follows h and its dialerProxy chain through byTag to the loopback it reaches:
// that loopback's inbound tag and the path ("x via y via lo"), or ok false. S1 bounds the chain;
// the walk stops after len(byTag) hops regardless.
func LoopbackReached(h Handler, byTag map[string]Handler) (inbound, path string, ok bool) {
	var hops []string
	for i := 0; h != nil && i <= len(byTag); i++ {
		hops = append(hops, HandlerName(h))
		view, isView := h.(LoopView)
		if !isView {
			return "", "", false
		}
		if tag, loop := view.LoopbackInbound(); loop {
			return tag, strings.Join(hops, " via "), true
		}
		next := view.DialerProxy()
		if next == "" {
			return "", "", false
		}
		h = byTag[next]
	}
	return "", "", false
}

// HandlerName is a handler's tag, or "(untagged)" for the default outbound without one.
func HandlerName(h Handler) string {
	if tag := h.Tag(); tag != "" {
		return tag
	}
	return "(untagged)"
}
