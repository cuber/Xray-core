package hysteria

import (
	"context"
	go_errors "errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// A session owner's connection pool (Core spec024 R4, R5, R6). Each member is a client: one connection
// with today's dial, authentication and relay negotiation. The pool chooses a member per stream or UDP
// session, scales by open streams and never moves a flow between members.
const (
	// The pool drains one connection when the open streams stay at or below this share of the target of
	// one connection fewer, for poolScaleDownAfter.
	poolScaleDownShare = 0.25
	poolScaleDownAfter = 60 * time.Second
	// A drained connection without streams is kept this long as a spare.
	poolSpareFor = 5 * time.Minute
	// A connection with bytes in flight and no packet acknowledged for this long is suspect.
	poolStallAfter = time.Second
	// Redials of connections the pool needs back off exponentially with full jitter.
	poolRedialMin = 100 * time.Millisecond
	poolRedialMax = 5 * time.Second
	// How long opening a stream waits for credit when no connection has any (Xray's handshake timeout).
	poolStreamWait = 4 * time.Second
)

type memberState int

const (
	memberActive memberState = iota
	memberDraining
	memberSpare
)

func (s memberState) String() string {
	switch s {
	case memberDraining:
		return "draining"
	case memberSpare:
		return "spare"
	default:
		return "active"
	}
}

type member struct {
	c       *client
	state   memberState // guarded by the pool's lock
	streams atomic.Int64
	spareAt time.Time

	// Stall detection at each maintenance tick, under the pool's lock: the acknowledged packets at the last
	// tick, and when they last grew or nothing was in flight.
	lastAcked  uint64
	progressAt time.Time
	suspect    atomic.Bool
}

func (m *member) live() bool {
	return m.c.liveStatus() == StatusActive
}

// open opens a stream or UDP session on the member, dialing it when it has no connection.
func (m *member) open(ctx context.Context, datagram, wait bool) (stat.Connection, error) {
	if datagram {
		return m.c.udp(ctx)
	}
	conn, err := m.c.tcpOpen(ctx, wait)
	if err != nil {
		return nil, err
	}
	m.streams.Add(1)
	conn.release = func() { m.streams.Add(-1) }
	return conn, nil
}

// load is the member's open streams and UDP sessions.
func (m *member) load() int64 {
	return m.streams.Load() + int64(m.c.udpSessions())
}

type pool struct {
	mu        sync.Mutex
	session   *Session
	newMember func() *client
	members   []*member
	probeNext int
	lowSince  time.Time
	dialing   bool
	backoff   time.Duration
	redialAt  time.Time
	now       func() time.Time
	quicStats *internet.QuicStatsSet
}

func newPool(session *Session, quicStats *internet.QuicStatsSet, newMember func() *client) *pool {
	p := &pool{session: session, newMember: newMember, now: time.Now, quicStats: quicStats}
	quicStats.SetSession(p.report)
	return p
}

// add makes a member; called under the lock.
func (p *pool) add() *member {
	m := &member{c: p.newMember()}
	m.c.onConnect = func(conn internet.QuicConn) {
		p.quicStats.SetDetail(conn, func() internet.QuicConnDetail { return p.detail(m) })
	}
	p.members = append(p.members, m)
	return m
}

func (p *pool) active() []*member {
	var out []*member
	for _, m := range p.members {
		if m.state == memberActive {
			out = append(out, m)
		}
	}
	return out
}

// pick chooses the member for the next stream (R4) or probe (R6); called under the lock.
func (p *pool) pick(probe bool, skip map[*member]bool) *member {
	active := p.active()
	if len(active) == 0 {
		if reactivated := p.reactivate(); reactivated != nil {
			return reactivated
		}
		return p.add()
	}
	var live, healthy []*member
	for _, m := range active {
		if skip[m] || !m.live() {
			continue
		}
		live = append(live, m)
		if !m.suspect.Load() {
			healthy = append(healthy, m)
		}
	}
	if len(healthy) == 0 {
		healthy = live
	}
	if len(healthy) == 0 {
		// No connection: dial on the first active member, as a single connection would (R5).
		for _, m := range active {
			if !skip[m] {
				return m
			}
		}
		return nil
	}
	if probe {
		p.probeNext++
		return healthy[p.probeNext%len(healthy)]
	}
	best := healthy[0]
	for _, m := range healthy[1:] {
		if m.load() < best.load() {
			best = m
		}
	}
	return best
}

// reactivate brings back a draining connection, else a spare one; called under the lock.
func (p *pool) reactivate() *member {
	var spare *member
	for _, m := range p.members {
		switch {
		case m.state == memberDraining && m.live():
			m.state = memberActive
			return m
		case m.state == memberSpare && m.live() && spare == nil:
			spare = m
		}
	}
	if spare != nil {
		spare.state = memberActive
	}
	return spare
}

func (p *pool) open(ctx context.Context, datagram bool) (stat.Connection, error) {
	probe := session.ProbeFromContext(ctx)
	skip := make(map[*member]bool)
	var lastErr error
	for {
		p.mu.Lock()
		m := p.pick(probe, skip)
		p.mu.Unlock()
		if m == nil {
			break
		}
		skip[m] = true
		conn, err := m.open(ctx, datagram, false)
		if err == nil {
			p.scaleUp()
			return conn, nil
		}
		lastErr = err
		if !isStreamLimit(err) {
			return nil, err
		}
	}
	if datagram || lastErr == nil {
		return nil, lastErr
	}
	// No connection has stream credit: wait on the least loaded one (R4).
	p.mu.Lock()
	m := p.pick(false, nil)
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, poolStreamWait)
	defer cancel()
	conn, err := m.open(ctx, false, true)
	if err == nil {
		p.scaleUp()
	}
	return conn, err
}

func isStreamLimit(err error) bool {
	var limit *quic.StreamLimitReachedError
	return go_errors.As(err, &limit)
}

// scaleUp adds a connection when the active ones average scaleUpStreams (R4).
func (p *pool) scaleUp() {
	p.mu.Lock()
	defer p.mu.Unlock()
	active := p.active()
	var load int64
	for _, m := range active {
		load += m.load()
	}
	if len(active) > 0 && load < int64(p.session.ScaleUpStreams)*int64(len(active)) {
		return
	}
	if p.reactivate() != nil {
		return
	}
	if len(p.members) >= int(p.session.MaxConnections) || p.dialing {
		return
	}
	p.dialing = true
	m := p.add()
	go p.warm(m)
}

// warm dials a member in the background.
func (p *pool) warm(m *member) {
	err := m.c.connect()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialing = false
	if err != nil {
		errors.LogInfo(context.Background(), "hysteria session: a pool connection failed to dial: ", err)
		p.redialFailed()
		return
	}
	p.backoff = 0
}

// redialFailed backs off the next redial; called under the lock.
func (p *pool) redialFailed() {
	if p.backoff == 0 {
		p.backoff = poolRedialMin
	} else if p.backoff *= 2; p.backoff > poolRedialMax {
		p.backoff = poolRedialMax
	}
	p.redialAt = p.now().Add(time.Duration(rand.Int63n(int64(p.backoff) + 1)))
}

// maintain runs on every cleanup tick: closed connections, stalls, drain and spare, and the minimum (R4, R5).
func (p *pool) maintain() {
	for _, m := range p.snapshot() {
		m.c.clean(false)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()

	kept := p.members[:0]
	for _, m := range p.members {
		if !m.live() && m.state != memberActive {
			m.c.clean(true)
			continue // a draining or spare connection that closed is gone
		}
		if m.state == memberSpare && now.Sub(m.spareAt) >= poolSpareFor {
			m.c.clean(true)
			continue
		}
		kept = append(kept, m)
	}
	p.members = kept

	for _, m := range p.members {
		p.checkProgress(m, now)
		if m.state == memberDraining && m.load() == 0 {
			m.state, m.spareAt = memberSpare, now
		}
	}

	active := p.active()
	var load int64
	for _, m := range active {
		load += m.load()
	}
	threshold := float64(int64(len(active)-1)*int64(p.session.StreamsPerConnection)) * poolScaleDownShare
	switch {
	case len(active) <= int(p.session.MinConnections) || float64(load) > threshold:
		p.lowSince = time.Time{}
	case p.lowSince.IsZero():
		p.lowSince = now
	case now.Sub(p.lowSince) >= poolScaleDownAfter:
		least := active[0]
		for _, m := range active[1:] {
			if m.load() < least.load() {
				least = m
			}
		}
		least.state = memberDraining
		p.lowSince = time.Time{}
	}

	// Keep the minimum: dial a member back once it is needed and the backoff allows.
	live := 0
	for _, m := range p.active() {
		if m.live() {
			live++
		}
	}
	if live < int(p.session.MinConnections) && !p.dialing && !now.Before(p.redialAt) && len(p.members) > 0 {
		for _, m := range p.active() {
			if !m.live() {
				p.dialing = true
				go p.warm(m)
				break
			}
		}
	}
}

// checkProgress marks a connection suspect when it had bytes in flight and no packet acknowledged for
// poolStallAfter (R5), from the library's own counters (quic-go fork spec002).
func (p *pool) checkProgress(m *member, now time.Time) {
	conn := m.c.live.Load()
	if conn == nil || !m.live() {
		m.suspect.Store(false)
		m.progressAt = time.Time{}
		return
	}
	stats := quicrecovery.Conn(conn).QuicStats()
	switch {
	case m.progressAt.IsZero() || stats.PacketsAcked != m.lastAcked || stats.BytesInFlight == 0:
		m.progressAt = now
		m.suspect.Store(false)
	case now.Sub(m.progressAt) >= poolStallAfter:
		m.suspect.Store(true)
	}
	m.lastAcked = stats.PacketsAcked
}

func (p *pool) snapshot() []*member {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*member(nil), p.members...)
}

// clean closes every member when the instance closes.
func (p *pool) clean(force bool) {
	if force {
		for _, m := range p.snapshot() {
			m.c.clean(true)
		}
		return
	}
	p.maintain()
}

// relayStatus reads the binding from a live member: all members negotiate the same list.
func (p *pool) relayStatus(index uint32) (status, user string) {
	for _, m := range p.snapshot() {
		if m.live() {
			return m.c.relayStatus(index)
		}
	}
	return RelayNoConnection, ""
}

func (p *pool) detail(m *member) internet.QuicConnDetail {
	p.mu.Lock()
	state := m.state.String()
	p.mu.Unlock()
	if state == "active" && m.suspect.Load() {
		state = "suspect"
	}
	return internet.QuicConnDetail{State: state, StreamsOpen: uint64(m.load()), Relay: m.c.relayReport()}
}

func (p *pool) report() internet.QuicSessionReport {
	r := internet.QuicSessionReport{Min: p.session.MinConnections, Max: p.session.MaxConnections}
	for _, m := range p.snapshot() {
		p.mu.Lock()
		state := m.state
		p.mu.Unlock()
		r.StreamsOpen += uint64(m.load())
		switch {
		case state == memberDraining:
			r.Draining++
		case state == memberSpare:
			r.Spare++
		case m.suspect.Load():
			r.Suspect++
		case m.live():
			r.Active++
		}
	}
	return r
}
