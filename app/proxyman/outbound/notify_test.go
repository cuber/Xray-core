package outbound_test

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/proxyman/outbound"
)

// Core spec027 E20: adding and removing a handler notifies, after the manager's locks are released.
func TestManagerNotifiesHandlerChanges(t *testing.T) {
	ctx := context.Background()
	m, err := outbound.New(ctx, &proxyman.OutboundConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var seen []int
	cancel := m.OnChange(func() { seen = append(seen, len(m.Select([]string{"node"}))) })
	defer cancel()
	if err := m.AddHandler(ctx, &recordingHandler{tag: "node-a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddHandler(ctx, &recordingHandler{tag: "node-a"}); err == nil {
		t.Fatal("a duplicate tag was added")
	}
	if err := m.RemoveHandler(ctx, "node-a"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 0 {
		t.Fatalf("notifications saw %v, want [1 0] (a refused add does not notify)", seen)
	}
}
