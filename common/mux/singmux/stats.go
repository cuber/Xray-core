package singmux

import (
	"sync/atomic"

	sm "github.com/sagernet/sing-mux"
	"github.com/xtls/xray-core/common/net"
)

// Refusal reasons reported by the control plane.
const (
	rejectPreamble = "preamble"
	rejectClosed   = "closed"
	rejectLimit    = "limit"
	rejectRevoked  = "revoked"
	rejectInvalid  = "invalid"
	rejectNested   = "nested"
	rejectDispatch = "dispatch"
)

var (
	protocolNames = [...]string{sm.ProtocolSmux: "smux", sm.ProtocolYAMux: "yamux", sm.ProtocolH2Mux: "h2mux"}
	rejectReasons = [...]string{rejectPreamble, rejectClosed, rejectLimit, rejectRevoked, rejectInvalid, rejectNested, rejectDispatch}
)

type counters struct {
	carriers      [len(protocolNames)]atomic.Int64
	padded        atomic.Int64
	tcp           atomic.Int64
	udp           atomic.Int64
	totalCarriers atomic.Uint64
	totalStreams  atomic.Uint64
	rejected      [len(rejectReasons)]atomic.Uint64
}

// Snapshot is the runtime state of one inbound's sing-mux server.
type Snapshot struct {
	Enabled          bool
	MaxStreams       uint32
	ActiveCarriers   map[string]uint64
	ActivePadded     uint64
	ActiveTCPStreams uint64
	ActiveUDPStreams uint64
	TotalCarriers    uint64
	TotalStreams     uint64
	Rejected         map[string]uint64
}

func (c *counters) openCarrier(protocol byte, padded bool) {
	c.carriers[protocol].Add(1)
	if padded {
		c.padded.Add(1)
	}
	c.totalCarriers.Add(1)
}

func (c *counters) closeCarrier(protocol byte, padded bool) {
	c.carriers[protocol].Add(-1)
	if padded {
		c.padded.Add(-1)
	}
}

func (c *counters) openStream(network net.Network) {
	if network == net.Network_UDP {
		c.udp.Add(1)
	} else {
		c.tcp.Add(1)
	}
	c.totalStreams.Add(1)
}

func (c *counters) closeStream(network net.Network) {
	if network == net.Network_UDP {
		c.udp.Add(-1)
	} else {
		c.tcp.Add(-1)
	}
}

func (c *counters) reject(reason string) {
	for i, name := range rejectReasons {
		if name == reason {
			c.rejected[i].Add(1)
			return
		}
	}
}

// Snapshot reports the loaded options and current counters.
func (s *Server) Snapshot() Snapshot {
	snapshot := Snapshot{
		Enabled:          s.options.Enabled,
		MaxStreams:       s.options.MaxStreams,
		ActiveCarriers:   make(map[string]uint64, len(protocolNames)),
		ActivePadded:     gauge(s.stats.padded.Load()),
		ActiveTCPStreams: gauge(s.stats.tcp.Load()),
		ActiveUDPStreams: gauge(s.stats.udp.Load()),
		TotalCarriers:    s.stats.totalCarriers.Load(),
		TotalStreams:     s.stats.totalStreams.Load(),
		Rejected:         make(map[string]uint64, len(rejectReasons)),
	}
	for i, name := range protocolNames {
		snapshot.ActiveCarriers[name] = gauge(s.stats.carriers[i].Load())
	}
	for i, name := range rejectReasons {
		snapshot.Rejected[name] = s.stats.rejected[i].Load()
	}
	return snapshot
}

func gauge(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
