package routing

import "github.com/xtls/xray-core/common/errors"

type BalancerOverrider interface {
	SetOverrideTarget(tag, target string) error
	GetOverrideTarget(tag string) (string, error)
}

type BalancerPrincipleTarget interface {
	GetPrincipleTarget(tag string) ([]string, error)
}

// BalancerFallback reports a balancer's fallbackTag as loaded and, when its
// strategy reports it, whether new picks currently end on it (Core spec016).
type BalancerFallback interface {
	GetFallback(tag string) (fallbackTag string, active bool, reported bool, err error)
}

// FallbackHop is one level of a balancer's fallback chain (spec017).
type FallbackHop struct {
	Tag string
	// Balancer: Tag names a balancer of the router; false: an outbound tag, which ends the chain.
	Balancer bool
	// Active: a balancer hop's strategy yields no candidate now, so its picks use its fallback (an
	// override on that hop still wins), as BalancerFallback's active.
	Active bool
	// Reported: the hop's strategy reports Active.
	Reported bool
}

// BalancerFallbackChain resolves the chain after a balancer: hop 0 is its fallbackTag; the last hop
// is an outbound, or a balancer without a fallbackTag; none when it has no fallbackTag (spec017).
type BalancerFallbackChain interface {
	GetFallbackChain(tag string) ([]FallbackHop, error)
}

// BalancerView reads balancers from one published balancer map, so a chain never mixes two maps
// (spec017 L-E).
type BalancerView interface {
	// Tags lists the balancers in tag order.
	Tags() []string
	GetOverrideTarget(tag string) (string, error)
	BalancerPrincipleTarget
	BalancerFallback
	BalancerFallbackChain
}

// BalancerLister gives a view of the current balancer map (spec017).
type BalancerLister interface {
	Balancers() BalancerView
}

// LoopGuard counts what the routing loop guards stopped since start (spec017 L8).
type LoopGuard interface {
	RecordLoopbackReentry()
	LoopGuards() (fallbackChain uint64, loopbackReentry uint64)
}

// ErrRouteDropped is returned when a loop guard stops a connection: the dispatcher closes it
// instead of taking the default outbound (spec017 L6, L7).
var ErrRouteDropped = errors.New("route dropped by a loop guard")
