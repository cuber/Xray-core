package dispatcher_test

import (
	"strings"
	"testing"

	. "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	stats "github.com/xtls/xray-core/features/stats"
)

type TestCounter int64

func (c *TestCounter) Value() int64 {
	return int64(*c)
}

func (c *TestCounter) Add(v int64) int64 {
	x := int64(*c) + v
	*c = TestCounter(x)
	return x
}

func (c *TestCounter) Set(v int64) int64 {
	*c = TestCounter(v)
	return v
}

func TestStatsWriter(t *testing.T) {
	var c TestCounter
	writer := &SizeStatWriter{
		Counter: &c,
		Writer:  buf.Discard,
	}

	mb := buf.MergeBytes(nil, []byte("abcd"))
	common.Must(writer.WriteMultiBuffer(mb))

	mb = buf.MergeBytes(nil, []byte("efg"))
	common.Must(writer.WriteMultiBuffer(mb))

	if c.Value() != 7 {
		t.Fatal("unexpected counter value. want 7, but got ", c.Value())
	}
}

type domainTrafficRecorder struct {
	domain        string
	uplinkBytes   uint64
	downlinkBytes uint64
}

func (r *domainTrafficRecorder) DomainTrafficEnabled() bool { return true }

func (r *domainTrafficRecorder) RecordDomainTraffic(domain string, uplinkBytes, downlinkBytes uint64) {
	r.domain = domain
	r.uplinkBytes += uplinkBytes
	r.downlinkBytes += downlinkBytes
}

func (*domainTrafficRecorder) DomainTrafficBuckets(string, uint64, uint32) stats.DomainTrafficSnapshot {
	return stats.DomainTrafficSnapshot{}
}

func TestDomainTrafficWriterUsesResolvedRouteDomain(t *testing.T) {
	recorder := new(domainTrafficRecorder)
	writer := &DomainTrafficWriter{
		Recorder: recorder,
		Outbound: []*session.Outbound{{
			Target:      xnet.TCPDestination(xnet.IPAddress([]byte{127, 0, 0, 1}), 443),
			RouteTarget: xnet.TCPDestination(xnet.DomainAddress("Example.COM"), 443),
		}},
		Uplink: true,
		Writer: buf.Discard,
	}
	common.Must(writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("payload"))))
	if recorder.domain != "Example.COM" || recorder.uplinkBytes != 7 || recorder.downlinkBytes != 0 {
		t.Fatalf("unexpected domain traffic record: %+v", recorder)
	}
}

func TestDomainTrafficReaderCountsUplink(t *testing.T) {
	recorder := new(domainTrafficRecorder)
	reader := &DomainTrafficReader{
		Recorder: recorder,
		Outbound: []*session.Outbound{{
			OriginalTarget: xnet.TCPDestination(xnet.DomainAddress("example.com"), 443),
		}},
		Reader: buf.NewReader(strings.NewReader("payload")),
	}
	mb, err := reader.ReadMultiBuffer()
	common.Must(err)
	buf.ReleaseMulti(mb)
	if recorder.domain != "example.com" || recorder.uplinkBytes != 7 || recorder.downlinkBytes != 0 {
		t.Fatalf("unexpected domain traffic record: %+v", recorder)
	}
}
