package internet

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/xtls/xray-core/common/net"
)

// spec021 R3: a handler's set of QUIC connections.

type fakeQuicConn struct {
	id      int
	packets *atomic.Uint64
	done    *atomic.Bool
}

func newFakeQuicConn(id int) fakeQuicConn {
	return fakeQuicConn{id: id, packets: new(atomic.Uint64), done: new(atomic.Bool)}
}

func (c fakeQuicConn) QuicStats() QuicConnStats {
	return QuicConnStats{Counters: QuicCounters{PacketsSent: c.packets.Load(), PacketsLost: c.packets.Load() / 10}}
}
func (c fakeQuicConn) Done() bool { return c.done.Load() }
func (c fakeQuicConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IP{192, 0, 2, 1}, Port: 1000 + c.id}
}

func TestQuicStatsSetNil(t *testing.T) {
	var s *QuicStatsSet
	c := newFakeQuicConn(0)
	s.Add(c)
	s.SetUser(c, "u")
	s.Remove(c)
	if r := s.Report(true); r.ConnectionsTotal != 0 || len(r.Connections) != 0 {
		t.Fatalf("a nil set reports nothing: %+v", r)
	}
}

func TestQuicStatsSetTotalsAcrossReplacement(t *testing.T) {
	var s QuicStatsSet
	a := newFakeQuicConn(1)
	s.Add(a)
	s.Add(a) // registering twice is a no-op
	s.SetUser(a, "user@example")
	a.packets.Store(100)
	r := s.Report(true)
	if r.Totals.PacketsSent != 100 || r.Totals.PacketsLost != 10 || r.ConnectionsActive != 1 || r.ConnectionsTotal != 1 {
		t.Fatalf("one live connection: %+v", r)
	}
	if len(r.Connections) != 1 || r.Connections[0].User != "user@example" || r.Connections[0].Remote != "192.0.2.1:1001" {
		t.Fatalf("its row: %+v", r.Connections)
	}

	// the connection is replaced: its counters stay in the totals
	a.packets.Store(150)
	a.done.Store(true)
	if r := s.Report(true); r.ConnectionsActive != 0 || len(r.Connections) != 0 || r.Totals.PacketsSent != 150 {
		t.Fatalf("a closed connection still registered: %+v", r)
	}
	s.Remove(a)
	s.Remove(a)
	b := newFakeQuicConn(2)
	s.Add(b)
	b.packets.Store(30)
	r = s.Report(false)
	if r.Totals.PacketsSent != 180 || r.Totals.PacketsLost != 15+3 || r.ConnectionsActive != 1 || r.ConnectionsTotal != 2 || r.Connections != nil {
		t.Fatalf("totals after replacement: %+v", r)
	}
}

func TestQuicStatsSetRowsBounded(t *testing.T) {
	var s QuicStatsSet
	for i := range MaxQuicConnectionRows + 5 {
		s.Add(newFakeQuicConn(i))
	}
	r := s.Report(true)
	if len(r.Connections) != MaxQuicConnectionRows || !r.Truncated || r.ConnectionsActive != MaxQuicConnectionRows+5 {
		t.Fatalf("got %d rows, truncated %t, %d active", len(r.Connections), r.Truncated, r.ConnectionsActive)
	}
	if first := r.Connections[0].Remote; first != fmt.Sprintf("192.0.2.1:%d", 1000) {
		t.Fatalf("rows are oldest first, got %s", first)
	}
}

func TestMemoryStreamConfigHasQuicStats(t *testing.T) {
	mss, err := ToMemoryStreamConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mss.QuicStats == nil {
		t.Fatal("every stream settings value collects its QUIC connections")
	}
}
