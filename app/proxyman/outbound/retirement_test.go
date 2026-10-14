package outbound_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	impl "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/features/outbound"
)

type retiringTestHandler struct {
	recordingHandler
	manager   *impl.Manager
	done      chan struct{}
	once      sync.Once
	retired   atomic.Bool
	closed    atomic.Bool
	failStart bool
}

func (h *retiringTestHandler) Start() error {
	if h.failStart {
		return fmt.Errorf("start failed")
	}
	return nil
}
func (h *retiringTestHandler) Retirement() outbound.Retirement { return h }
func (h *retiringTestHandler) Retire() <-chan struct{} {
	h.manager.GetHandler("replacement") // Must not be called under registry lock.
	h.retired.Store(true)
	return h.done
}
func (h *retiringTestHandler) Close() error {
	h.manager.GetHandler("replacement")
	h.closed.Store(true)
	h.once.Do(func() { close(h.done) })
	return nil
}

func TestRemovedDrainingHandlerStillOwnedByShutdown(t *testing.T) {
	m, _ := impl.New(context.Background(), &proxyman.OutboundConfig{})
	h := &retiringTestHandler{recordingHandler: recordingHandler{tag: "drain"}, manager: m, done: make(chan struct{})}
	if err := m.AddHandler(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveHandler(context.Background(), h.Tag()); err != nil {
		t.Fatal(err)
	}
	if m.GetHandler(h.Tag()) != nil || !h.retired.Load() || h.closed.Load() {
		t.Fatal("removal failed to retire")
	}
	replacement := &recordingHandler{tag: "drain"}
	if err := m.AddHandler(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case err := <-done:
		if err != nil || !h.closed.Load() {
			t.Fatal("removed handler not closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown held registry lock while closing")
	}
}

func TestFailedStartDoesNotPublishOutbound(t *testing.T) {
	m, _ := impl.New(context.Background(), &proxyman.OutboundConfig{})
	m.Start()
	defer m.Close()
	h := &retiringTestHandler{recordingHandler: recordingHandler{tag: "failed"}, manager: m, done: make(chan struct{}), failStart: true}
	if err := m.AddHandler(context.Background(), h); err == nil {
		t.Fatal("failed start accepted")
	}
	if m.GetHandler(h.Tag()) != nil || m.GetDefaultHandler() != nil {
		t.Fatal("failed handler published")
	}
}

func TestShutdownRejectsReplacement(t *testing.T) {
	m, _ := impl.New(context.Background(), &proxyman.OutboundConfig{})
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.AddHandler(context.Background(), &recordingHandler{tag: "replacement"}); err == nil {
		t.Fatal("published after shutdown")
	}
	if m.GetHandler("replacement") != nil {
		t.Fatal("replacement escaped shutdown")
	}
	if err := m.Start(); err == nil {
		t.Fatal("closed manager restarted")
	}
}
