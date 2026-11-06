package router

import (
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/log"
	xlua "github.com/xtls/xray-core/common/lua"
	"github.com/xtls/xray-core/features/routing"
	lua "github.com/yuin/gopher-lua"
)

const scriptExecutionTimeout = 6 * time.Second

type scriptEngine struct {
	program *xlua.Program
	// pool is published by start, which runs with the router's Start, possibly after inbounds already
	// route connections (Core spec007 FR-004).
	pool atomic.Pointer[xlua.Pool]
}

func newScriptEngine(script string) (*scriptEngine, error) {
	program, err := xlua.Compile(script)
	if err != nil {
		return nil, err
	}
	return &scriptEngine{program: program}, nil
}

func (e *scriptEngine) start(router *Router) error {
	pool, err := xlua.NewPool(router.ctx, scriptExecutionTimeout, e.program.NewStateFactory(
		scriptExecutionTimeout*20,
		func(L *lua.LState) {
			geodata.RegisterLua(L)
			log.RegisterLua(L)
			router.RegisterLua(L)
			dns.RegisterLua(L, router.dns)
		},
		func(L *lua.LState) error {
			if L.GetGlobal("HandleRoute").Type() != lua.LTFunction {
				return errors.New("routing script must define HandleRoute(...)")
			}
			return nil
		}))
	if err != nil {
		return err
	}

	e.pool.Store(pool)
	errors.LogInfo(router.ctx, "routing script initialized")
	return nil
}

func (e *scriptEngine) close() {
	if pool := e.pool.Load(); pool != nil {
		pool.Close()
	}
}

func (e *scriptEngine) pickRoute(ctx routing.Context) (routing.Route, error) {
	var outboundTag, ruleTag string
	var routeErr error

	pool := e.pool.Load()
	if pool == nil {
		// Not started yet: close the connection rather than send it to the default outbound.
		return nil, errors.New("routing script is not started yet").Base(routing.ErrRouteDropped)
	}
	if err := pool.WithState(nil, 0, func(L *lua.LState) error {
		if err := callLuaRoute(L, ctx); err != nil {
			return err
		}
		outboundTag, ruleTag, routeErr = readLuaRouteResult(L)
		return nil
	}); err != nil {
		return nil, err
	}

	if routeErr != nil {
		return nil, routeErr
	}
	if outboundTag == "" {
		return nil, common.ErrNoClue
	}

	return &Route{Context: ctx, outboundTag: outboundTag, ruleTag: ruleTag}, nil
}
