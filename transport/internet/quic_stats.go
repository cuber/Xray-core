package internet

import (
	"sort"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/net"
)

// MaxQuicConnectionRows bounds the connection rows of one handler's report (spec021 R3).
const MaxQuicConnectionRows = 256

// QuicCounters are the packet counters of QUIC connections (spec021 R3). Sent counts originals
// only; received counts processed packets, and duplicates received the ones dropped as such.
type QuicCounters struct {
	PacketsSent        uint64
	BytesSent          uint64
	PacketsReceived    uint64
	BytesReceived      uint64
	PacketsLost        uint64
	BytesLost          uint64
	SpuriousLosses     uint64
	PTOCount           uint64
	CopiesSent         uint64
	CopyBytesSent      uint64
	CopiesSkipped      uint64
	DuplicatesReceived uint64
}

func (c *QuicCounters) add(o QuicCounters) {
	c.PacketsSent += o.PacketsSent
	c.BytesSent += o.BytesSent
	c.PacketsReceived += o.PacketsReceived
	c.BytesReceived += o.BytesReceived
	c.PacketsLost += o.PacketsLost
	c.BytesLost += o.BytesLost
	c.SpuriousLosses += o.SpuriousLosses
	c.PTOCount += o.PTOCount
	c.CopiesSent += o.CopiesSent
	c.CopyBytesSent += o.CopyBytesSent
	c.CopiesSkipped += o.CopiesSkipped
	c.DuplicatesReceived += o.DuplicatesReceived
}

// QuicConnStats is one connection's RTT estimate and counters.
type QuicConnStats struct {
	SmoothedRTT time.Duration
	MinRTT      time.Duration
	LatestRTT   time.Duration
	RTTVariance time.Duration
	Counters    QuicCounters
}

// QuicConn is a QUIC connection as a transport registers it. Its value is the set's key.
type QuicConn interface {
	QuicStats() QuicConnStats
	Done() bool
	RemoteAddr() net.Addr
}

// QuicConnection is one live connection of a report.
type QuicConnection struct {
	Remote string
	User   string // an inbound connection's authenticated user, by email
	Age    time.Duration
	QuicConnStats
}

// QuicReport is what a QuicStatsSet reports.
type QuicReport struct {
	Totals            QuicCounters
	ConnectionsActive uint64
	ConnectionsTotal  uint64
	Connections       []QuicConnection
	Truncated         bool
}

// QuicStatsSet collects the QUIC connections built from one stream settings value: a handler's
// connections, whichever of its listeners or dialers made them. Its methods accept a nil set.
type QuicStatsSet struct {
	mu     sync.Mutex
	closed QuicCounters // the final counters of removed connections
	total  uint64
	conns  map[QuicConn]*quicConnEntry
}

type quicConnEntry struct {
	user  string
	start time.Time
}

// Add registers a new connection.
func (s *QuicStatsSet) Add(c QuicConn) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = make(map[QuicConn]*quicConnEntry)
	}
	if _, ok := s.conns[c]; ok {
		return
	}
	s.conns[c] = &quicConnEntry{start: time.Now()}
	s.total++
}

// SetUser records the user an inbound connection authenticated as.
func (s *QuicStatsSet) SetUser(c QuicConn, user string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.conns[c]; ok {
		e.user = user
	}
}

// Remove folds a connection's counters into the totals and forgets it.
func (s *QuicStatsSet) Remove(c QuicConn) {
	if s == nil {
		return
	}
	stats := c.QuicStats()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conns[c]; !ok {
		return
	}
	delete(s.conns, c)
	s.closed.add(stats.Counters)
}

// Report returns the totals since the set was created and, with rows, the live connections, oldest first.
// The lock is held only to copy the set; counters are read after.
func (s *QuicStatsSet) Report(rows bool) QuicReport {
	var r QuicReport
	if s == nil {
		return r
	}
	type live struct {
		conn QuicConn
		quicConnEntry
	}
	s.mu.Lock()
	r.Totals = s.closed
	r.ConnectionsTotal = s.total
	conns := make([]live, 0, len(s.conns))
	for c, e := range s.conns {
		conns = append(conns, live{conn: c, quicConnEntry: *e})
	}
	s.mu.Unlock()

	sort.Slice(conns, func(i, j int) bool { return conns[i].start.Before(conns[j].start) })
	now := time.Now()
	for _, c := range conns {
		stats := c.conn.QuicStats()
		r.Totals.add(stats.Counters)
		if c.conn.Done() {
			continue
		}
		r.ConnectionsActive++
		if !rows {
			continue
		}
		if len(r.Connections) == MaxQuicConnectionRows {
			r.Truncated = true
			continue
		}
		var remote string
		if addr := c.conn.RemoteAddr(); addr != nil {
			remote = addr.String()
		}
		r.Connections = append(r.Connections, QuicConnection{Remote: remote, User: c.user, Age: now.Sub(c.start), QuicConnStats: stats})
	}
	return r
}
