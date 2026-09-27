package dispatcher

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	coreStats "github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/transport"
)

type trafficContractStats struct {
	*coreStats.Manager
	entries map[[2]string][2]uint64
}

func newTrafficContractStats(t *testing.T) *trafficContractStats {
	t.Helper()
	m, err := coreStats.NewManager(context.Background(), &coreStats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return &trafficContractStats{Manager: m, entries: make(map[[2]string][2]uint64)}
}
func (*trafficContractStats) DomainTrafficEnabled() bool { return true }
func (s *trafficContractStats) RecordDomainTraffic(domain string, up, down uint64, users ...string) {
	user := ""
	if len(users) > 0 {
		user = users[0]
	}
	k := [2]string{user, domain}
	v := s.entries[k]
	v[0] += up
	v[1] += down
	s.entries[k] = v
}

type trafficContractReader struct {
	payload []byte
	err     error
}

func (r *trafficContractReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.payload == nil {
		return nil, io.EOF
	}
	payload := r.payload
	r.payload = nil
	return buf.MergeBytes(nil, payload), r.err
}
func (r *trafficContractReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return r.ReadMultiBuffer()
}

type trafficContractWriter struct {
	t     *testing.T
	stats *trafficContractStats
	key   [2]string
	want  uint64
	err   error
}

func (w *trafficContractWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	defer buf.ReleaseMulti(mb)
	if w.stats.entries[w.key][1] != w.want {
		w.t.Fatal("downlink not accounted before writer result")
	}
	return w.err
}

type trafficContractPolicy struct{ policy.Manager }

func (trafficContractPolicy) ForLevel(uint32) policy.Session {
	return policy.Session{Stats: policy.Stats{UserUplink: true, UserDownlink: true}}
}

func TestDomainTrafficByteOracleErrorsAndWriterFailure(t *testing.T) {
	for _, readErr := range []error{nil, io.EOF, context.DeadlineExceeded} {
		for _, writeErr := range []error{nil, io.ErrClosedPipe} {
			t.Run(errorLabel(readErr)+"/"+errorLabel(writeErr), func(t *testing.T) {
				s := newTrafficContractStats(t)
				for _, item := range []struct {
					user, domain string
					up, down     int
				}{{"alice", "example.test", 100, 200}, {"bob", "example.test", 300, 400}, {"alice", "other.test", 50, 60}} {
					ob := []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress(item.domain), 443)}}
					r := &DomainTrafficReader{User: item.user, Recorder: s, Outbound: ob, Reader: &trafficContractReader{payload: make([]byte, item.up), err: readErr}}
					mb, err := r.ReadMultiBuffer()
					if !errors.Is(err, readErr) || int(mb.Len()) != item.up {
						buf.ReleaseMulti(mb)
						t.Fatalf("reader data/error: %v", err)
					}
					buf.ReleaseMulti(mb)
					key := [2]string{item.user, item.domain}
					w := &DomainTrafficWriter{User: item.user, Recorder: s, Outbound: ob, Writer: &trafficContractWriter{t: t, stats: s, key: key, want: uint64(item.down), err: writeErr}}
					if err = w.WriteMultiBuffer(buf.MergeBytes(nil, make([]byte, item.down))); !errors.Is(err, writeErr) {
						t.Fatalf("writer error changed: %v", err)
					}
					if got := s.entries[key]; got != [2]uint64{uint64(item.up), uint64(item.down)} {
						t.Fatalf("identity oracle %v=%v", key, got)
					}
				}
				var up, down uint64
				for _, v := range s.entries {
					up += v[0]
					down += v[1]
				}
				if up != 450 || down != 660 || len(s.entries) != 3 {
					t.Fatalf("byte oracle %d/%d", up, down)
				}
			})
		}
	}
}

func errorLabel(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}

func TestDomainTrafficTimeoutDataAndEmpty(t *testing.T) {
	s := newTrafficContractStats(t)
	r := &DomainTrafficReader{User: "alice", Recorder: s, Reader: &trafficContractReader{payload: make([]byte, 100), err: context.DeadlineExceeded}}
	mb, err := r.ReadMultiBufferTimeout(time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || mb.Len() != 100 {
		buf.ReleaseMulti(mb)
		t.Fatal("timeout discarded data/error")
	}
	buf.ReleaseMulti(mb)
	mb, err = r.ReadMultiBufferTimeout(time.Second)
	buf.ReleaseMulti(mb)
	if err != io.EOF || s.entries[[2]string{"alice", "unknown"}] != [2]uint64{100, 0} {
		t.Fatal("empty read added traffic")
	}
	r.Reader = buf.NewReader(&emptyContractReader{})
	if _, err = r.ReadMultiBufferTimeout(time.Second); err != buf.ErrNotTimeoutReader {
		t.Fatalf("non-timeout reader: %v", err)
	}
}

type emptyContractReader struct{}

func (*emptyContractReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestDomainTrafficAttributionPriority(t *testing.T) {
	domain := func(s string) xnet.Destination { return xnet.TCPDestination(xnet.DomainAddress(s), 443) }
	ip := xnet.TCPDestination(xnet.LocalHostIP, 443)
	for _, tc := range []struct {
		name      string
		outbounds []*session.Outbound
		want      string
	}{
		{"target", []*session.Outbound{{Target: domain("target.test"), RouteTarget: domain("route.test"), OriginalTarget: domain("original.test")}}, "target.test"},
		{"route", []*session.Outbound{{Target: ip, RouteTarget: domain("route.test"), OriginalTarget: domain("original.test")}}, "route.test"},
		{"original", []*session.Outbound{{Target: ip, RouteTarget: ip, OriginalTarget: domain("original.test")}}, "original.test"},
		{"ip", []*session.Outbound{{Target: ip, RouteTarget: ip, OriginalTarget: ip}}, "unknown"},
		{"empty", nil, "unknown"},
		{"last-hop", []*session.Outbound{{Target: domain("previous.test")}, {Target: domain("last.test")}}, "last.test"},
		{"no-backtracking", []*session.Outbound{{Target: domain("previous.test")}, {Target: ip}}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outboundDomain(tc.outbounds); got != tc.want {
				t.Fatalf("domain=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestDomainTrafficSniffCacheReplayCountsOnce(t *testing.T) {
	for _, readErr := range []error{nil, io.EOF, context.DeadlineExceeded} {
		t.Run(errorLabel(readErr), func(t *testing.T) {
			s := newTrafficContractStats(t)
			ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: &protocol.MemoryUser{Email: "alice"}})
			ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.DomainAddress("example.test"), 443)}})
			link := WrapLink(ctx, trafficContractPolicy{}, s, &transport.Link{Reader: &trafficContractReader{payload: make([]byte, 100), err: readErr}, Writer: buf.Discard})
			cache := &cachedReader{reader: link.Reader.(buf.TimeoutReader)}
			defer cache.Interrupt()
			b := buf.New()
			defer b.Release()
			if err := cache.Cache(b, time.Second); !errors.Is(err, readErr) {
				t.Fatalf("sniff error=%v want=%v", err, readErr)
			}
			mb, err := cache.ReadMultiBuffer()
			if err != nil || mb.Len() != 100 {
				buf.ReleaseMulti(mb)
				t.Fatalf("sniff replay lost payload: err=%v", err)
			}
			buf.ReleaseMulti(mb)
			mb, _ = cache.ReadMultiBuffer()
			buf.ReleaseMulti(mb)
			if err := link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, make([]byte, 200))); err != nil {
				t.Fatal(err)
			}
			if s.entries[[2]string{"alice", "example.test"}] != [2]uint64{100, 200} {
				t.Fatal("sniff/replay double-counted domain bytes")
			}
			if s.GetCounter("user>>>alice>>>traffic>>>uplink").Value() != 100 || s.GetCounter("user>>>alice>>>traffic>>>downlink").Value() != 200 {
				t.Fatal("sniff/replay altered complete user Stats")
			}
		})
	}
}
