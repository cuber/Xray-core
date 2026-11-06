package router

import (
	goerrors "errors"
	"testing"

	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
)

// Core spec007 FR-004: a routing script picks only once its router started; a connection that
// arrives before closes (ErrRouteDropped) instead of dereferencing the unpublished pool or taking the
// default outbound.
func TestRoutingScriptBeforeStart(t *testing.T) {
	e, err := newScriptEngine(writeRouteScript(t, `function HandleRoute() return "direct", "r" end`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pickRoute(&routing_session.Context{}); !goerrors.Is(err, routing.ErrRouteDropped) {
		t.Fatalf("pick before start = %v, want ErrRouteDropped", err)
	}
	e.close()
}
