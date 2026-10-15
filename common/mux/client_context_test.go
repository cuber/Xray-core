package mux_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

type contextRecordingProxy struct {
	started chan context.Context
	release chan struct{}
	joined  chan struct{}
}

func (p *contextRecordingProxy) Process(ctx context.Context, _ *transport.Link, _ internet.Dialer) error {
	defer close(p.joined)
	p.started <- ctx
	<-p.release
	return nil
}

func TestDialingWorkerFactoryContext(t *testing.T) {
	owners := []*core.Instance{{}, {}}
	for _, name := range []string{"legacy-nil", "first-owner", "second-owner"} {
		t.Run(name, func(t *testing.T) {
			var base context.Context
			var owner *core.Instance
			if name != "legacy-nil" {
				owner = owners[0]
				if name == "second-owner" {
					owner = owners[1]
				}
				base = context.WithValue(context.Background(), core.XrayKey(1), owner)
				cancelled, cancel := context.WithTimeout(base, time.Second)
				cancel()
				base = cancelled
			}
			p := &contextRecordingProxy{
				started: make(chan context.Context, 1),
				release: make(chan struct{}),
				joined:  make(chan struct{}),
			}
			factory := &mux.DialingWorkerFactory{Context: base, Proxy: p}
			worker, err := factory.Create()
			if err != nil {
				t.Fatal(err)
			}
			var release sync.Once
			t.Cleanup(func() {
				release.Do(func() { close(p.release) })
				_ = worker.Close()
				select {
				case <-p.joined:
				case <-time.After(time.Second):
					t.Error("proxy worker did not join")
				}
			})
			var ctx context.Context
			select {
			case ctx = <-p.started:
			case <-time.After(time.Second):
				t.Fatal("proxy worker did not start")
			}
			if got := core.FromContext(ctx); got != owner {
				t.Fatalf("worker owner = %p, want %p", got, owner)
			}
			if ctx.Err() != nil {
				t.Fatalf("worker inherited request cancellation: %v", ctx.Err())
			}
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("worker inherited request deadline")
			}
			outbounds := session.OutboundsFromContext(ctx)
			if len(outbounds) != 1 || outbounds[0].Target.Address.Domain() != "v1.mux.cool" {
				t.Fatalf("unexpected worker destination: %v", outbounds)
			}
			release.Do(func() { close(p.release) })
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("worker did not cancel its own context on return")
			}
		})
	}
}
