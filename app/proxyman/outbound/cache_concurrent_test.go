package outbound_test

import (
	"context"
	"sync"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	. "github.com/xtls/xray-core/app/proxyman/outbound"
)

func TestTagsCacheConcurrentInvalidation(t *testing.T) {
	m, err := New(context.Background(), &proxyman.OutboundConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				tags := m.Select([]string{"node"})
				if len(tags) > 1 || (len(tags) == 1 && tags[0] != "node-a") {
					t.Errorf("invalid snapshot: %v", tags)
					return
				}
			}
		})
	}
	defer func() { close(stop); readers.Wait() }()
	for range 1000 {
		if err := m.AddHandler(context.Background(), &recordingHandler{tag: "node-a"}); err != nil {
			t.Fatal(err)
		}
		if tags := m.Select([]string{"node"}); len(tags) != 1 || tags[0] != "node-a" {
			t.Fatalf("post-add: %v", tags)
		}
		removed := make(chan error, 1)
		go func() { removed <- m.RemoveHandler(context.Background(), "node-a") }()
		if err := <-removed; err != nil {
			t.Fatal(err)
		}
		if tags := m.Select([]string{"node"}); len(tags) != 0 {
			t.Fatalf("post-remove stale snapshot: %v", tags)
		}
	}
}
