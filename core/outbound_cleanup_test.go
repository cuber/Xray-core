package core_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var cleanupObjects sync.Map

type cleanupOutbound struct {
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	closes atomic.Int32
}

func (p *cleanupOutbound) Process(context.Context, *transport.Link, internet.Dialer) error {
	return fmt.Errorf("cleanup fixture does not forward")
}

func (p *cleanupOutbound) Close() error {
	p.closes.Add(1)
	p.once.Do(func() { close(p.stop) })
	<-p.done
	return nil
}

func init() {
	common.Must(common.RegisterConfig((*wrapperspb.StringValue)(nil), func(_ context.Context, config interface{}) (interface{}, error) {
		p := &cleanupOutbound{stop: make(chan struct{}), done: make(chan struct{})}
		go func() { <-p.stop; close(p.done) }()
		cleanupObjects.Store(config.(*wrapperspb.StringValue).Value, p)
		return p, nil
	}))
}

func TestRejectedOutboundClosesConstructedResources(t *testing.T) {
	s, err := core.New(&core.Config{App: []*serial.TypedMessage{serial.ToTypedMessage(&proxyman.OutboundConfig{})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	manager := s.GetFeature(outbound.ManagerType()).(outbound.Manager)
	add := func(name string) (*cleanupOutbound, error) {
		t.Helper()
		key := t.Name() + "/" + name
		err := core.AddOutboundHandler(s, &core.OutboundHandlerConfig{Tag: "same", ProxySettings: serial.ToTypedMessage(wrapperspb.String(key))})
		value, ok := cleanupObjects.LoadAndDelete(key)
		if !ok {
			t.Fatal("constructor did not run")
		}
		p := value.(*cleanupOutbound)
		t.Cleanup(func() { p.Close() })
		return p, err
	}
	assertClosed := func(p *cleanupOutbound) {
		t.Helper()
		select {
		case <-p.done:
		case <-time.After(time.Second):
			t.Fatal("rejected object worker was not joined")
		}
		if p.closes.Load() != 1 {
			t.Fatalf("close count=%d want=1", p.closes.Load())
		}
	}
	original, err := add("original")
	if err != nil {
		t.Fatal(err)
	}
	selected := manager.GetHandler("same")
	rejected, err := add("duplicate")
	if err == nil {
		t.Fatal("duplicate accepted")
	}
	assertClosed(rejected)
	if original.closes.Load() != 0 || manager.GetHandler("same") != selected || manager.GetDefaultHandler() != selected {
		t.Fatal("rejection changed the existing handler")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertClosed(original)
	rejected, err = add("after-shutdown")
	if err == nil {
		t.Fatal("closed manager accepted handler")
	}
	assertClosed(rejected)
}
