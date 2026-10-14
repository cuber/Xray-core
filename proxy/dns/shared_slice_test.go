package dns

import (
	"reflect"
	"sync"
	"testing"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	dnsfeature "github.com/xtls/xray-core/features/dns"
	"golang.org/x/net/dns/dnsmessage"
)

type sharedClient struct {
	dnsfeature.Client
	ips []net.IP
}

func (c *sharedClient) LookupIP(string, dnsfeature.IPOption) ([]net.IP, uint32, error) {
	return c.ips, 300, nil
}

type answerWriter struct {
	t    *testing.T
	kind dnsmessage.Type
}

func (w answerWriter) WriteMessage(b *buf.Buffer) error {
	defer b.Release()
	var msg dnsmessage.Message
	if err := msg.Unpack(b.Bytes()); err != nil {
		w.t.Error(err)
		return nil
	}
	if len(msg.Answers) != 1 || msg.Answers[0].Header.Type != w.kind || msg.Answers[0].Header.TTL != 300 {
		w.t.Errorf("bad answer: %+v", msg)
	}
	return nil
}
func TestSharedSliceNormalization(t *testing.T) {
	shared := []net.IP{net.ParseIP("192.0.2.1")}
	before := []net.IP{append(net.IP(nil), shared[0]...)}
	h := &Handler{client: &sharedClient{ips: shared}}
	var wg sync.WaitGroup
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		wg.Go(func() {
			for range 1000 {
				h.handleIPQuery(1, kind, "fixture.test.", answerWriter{t, kind}, nil)
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(shared, before) {
		t.Fatalf("cache mutated: %v -> %v", before, shared)
	}
}
