package routing

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
