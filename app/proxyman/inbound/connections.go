package inbound

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/mux"
	"github.com/xtls/xray-core/transport/internet"
)

// ConnectionCounts are an inbound's live objects, or one user's (Core spec026 R2).
type ConnectionCounts struct {
	TCP             uint64
	MuxCoolCarriers uint64
	SingMuxCarriers uint64
	Hy2Connections  uint64
	Hy2Streams      uint64
	Hy2UDPSessions  uint64
	UDPSessions     uint64
}

func (c *ConnectionCounts) addEntry(e *acceptedConnection) {
	switch e.kind {
	case connHy2Stream:
		c.Hy2Streams++
	case connHy2UDP:
		c.Hy2UDPSessions++
	default:
		c.TCP++
		switch mux.Carrier(e.carrier.Load()) {
		case mux.MuxCoolCarrier:
			c.MuxCoolCarriers++
		case mux.SingMuxCarrier:
			c.SingMuxCarriers++
		}
	}
}

// UserConnections is one user's row, under the email as first bound.
type UserConnections struct {
	Email string
	ConnectionCounts
}

// Connections is an inbound's report. Users is keyed by lowercased email.
type Connections struct {
	Counts  ConnectionCounts
	Unbound uint64
	Closing uint64
	Users   map[string]*UserConnections
}

func (r *Connections) user(email string) *ConnectionCounts {
	key := strings.ToLower(email)
	row, ok := r.Users[key]
	if !ok {
		row = &UserConnections{Email: email}
		r.Users[key] = row
	}
	return &row.ConnectionCounts
}

// userSessions is an inbound proxy whose UDP sessions are bound to users (SS-2022 multi-user).
type userSessions interface {
	UserSessions() map[string]uint64
	CloseUserSessions(match func(email string) bool) uint64
}

func (h *AlwaysOnInboundHandler) streamWorkers() []*tcpWorker {
	var workers []*tcpWorker
	for _, w := range h.workers {
		if tw, ok := w.(*tcpWorker); ok {
			workers = append(workers, tw)
		}
	}
	return workers
}

// Connections reports the inbound's live objects. Locks are held only to copy entries.
func (h *AlwaysOnInboundHandler) Connections() Connections {
	r := Connections{Users: make(map[string]*UserConnections)}
	for _, w := range h.streamWorkers() {
		for _, e := range w.ownership.entries() {
			if e.closing.Load() {
				r.Closing++
				continue
			}
			r.Counts.addEntry(e)
			email := e.user()
			if email == "" {
				r.Unbound++
				continue
			}
			r.user(email).addEntry(e)
		}
	}
	if h.stream != nil {
		users, closing := h.stream.QuicStats.Census()
		r.Closing += closing
		for user, n := range users {
			r.Counts.Hy2Connections += n
			if user == "" {
				r.Unbound += n
				continue
			}
			r.user(user).Hy2Connections += n
		}
	}
	if p, ok := h.proxy.(userSessions); ok {
		for email, n := range p.UserSessions() {
			r.Counts.UDPSessions += n
			r.user(email).UDPSessions += n
		}
	}
	return r
}

// CloseConnections closes the objects of the users that match selects (Core spec026 R3): stream
// entries are canceled, then closed; QUIC connections and UDP sessions are closed. Closes run
// concurrently outside every lock; those still running after bound are pending.
func (h *AlwaysOnInboundHandler) CloseConnections(match func(email string) bool, bound time.Duration) (closed ConnectionCounts, pending uint64) {
	var closers []func()
	for _, w := range h.streamWorkers() {
		for _, e := range w.ownership.claim(match) {
			closed.addEntry(e)
			e.cancel()
			conn := e.conn
			closers = append(closers, func() { conn.Close() })
		}
	}
	if h.stream != nil {
		for _, c := range h.stream.QuicStats.Claim(match) {
			closed.Hy2Connections++
			qc := c.(internet.QuicCloser)
			closers = append(closers, func() { qc.CloseByControl("closed by control plane") })
		}
	}
	if p, ok := h.proxy.(userSessions); ok {
		closed.UDPSessions = p.CloseUserSessions(match)
	}
	return closed, closeBounded(closers, bound)
}

// closeBounded runs every close at once and returns how many were still running after bound.
func closeBounded(closers []func(), bound time.Duration) uint64 {
	if len(closers) == 0 {
		return 0
	}
	var left atomic.Int64
	left.Store(int64(len(closers)))
	done := make(chan struct{})
	for _, c := range closers {
		go func() {
			c()
			if left.Add(-1) == 0 {
				close(done)
			}
		}()
	}
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case <-done:
		return 0
	case <-timer.C:
		return uint64(max(left.Load(), 0))
	}
}
