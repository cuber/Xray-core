package outbound_test

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/transport"
)

type recordingHandler struct {
	tag    string
	result chan bool
}

func (h *recordingHandler) Tag() string { return h.tag }
func (h *recordingHandler) Dispatch(_ context.Context, link *transport.Link) {
	_, ok := link.Reader.(buf.TimeoutReader)
	h.result <- ok
}
func (*recordingHandler) SenderSettings() *serial.TypedMessage { return nil }
func (*recordingHandler) ProxySettings() *serial.TypedMessage  { return nil }
func (*recordingHandler) Start() error                         { return nil }
func (*recordingHandler) Close() error                         { return nil }

func TestTagsCacheInvalidated(t *testing.T) {
	ctx := context.Background()
	m, err := outbound.New(ctx, &proxyman.OutboundConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for round := 0; round < 100; round++ {
		if got := m.Select([]string{"node"}); len(got) != 0 {
			t.Fatal(got)
		}
		if err := m.AddHandler(ctx, &recordingHandler{tag: "node-a"}); err != nil {
			t.Fatal(err)
		}
		if got := m.Select([]string{"node"}); len(got) != 1 || got[0] != "node-a" {
			t.Fatal(got)
		}
		if err := m.RemoveHandler(ctx, "node-a"); err != nil {
			t.Fatal(err)
		}
		if got := m.Select([]string{"node"}); len(got) != 0 {
			t.Fatal(got)
		}
	}
}
