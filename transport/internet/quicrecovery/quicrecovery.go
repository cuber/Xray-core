// Package quicrecovery applies spec021's loss recovery and redundancy settings to
// quic-go (the fork's spec001) and reports QUIC connections to the control plane.
package quicrecovery

import (
	"math"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

// Apply sets the R1 and R2 fields of p on c. A field p leaves unset keeps quic-go's behavior.
func Apply(c *quic.Config, p *internet.QuicParams) {
	if p == nil {
		return
	}
	c.MaxAckDelay = time.Duration(p.MaxAckDelayMs) * time.Millisecond
	c.AckElicitingThreshold = int(p.AckElicitingThreshold)
	c.InitialRTT = time.Duration(p.InitialRttMs) * time.Millisecond
	c.PTOBackoffLimit = int(p.PtoBackoffLimit)
	c.LossTimeThreshold = p.LossTimeThreshold
	c.LossPacketThreshold = int(p.LossPacketThreshold)
	c.DuplicateMaxSize = uint16(min(p.DuplicateMaxSize, math.MaxUint16))
	c.DuplicateTail = p.DuplicateTail
	c.DuplicateDelay = time.Duration(p.DuplicateDelayMs) * time.Millisecond
}

// Effective returns the values that connections built from p use, defaults filled in.
func Effective(p *internet.QuicParams) quic.LossRecovery {
	var c quic.Config
	Apply(&c, p)
	return c.LossRecovery()
}

// Conn adapts a QUIC connection for an internet.QuicStatsSet.
func Conn(c *quic.Conn) internet.QuicConn { return conn{c: c} }

type conn struct{ c *quic.Conn }

func (c conn) QuicStats() internet.QuicConnStats {
	s := c.c.ConnectionStats()
	return internet.QuicConnStats{
		SmoothedRTT: s.SmoothedRTT,
		MinRTT:      s.MinRTT,
		LatestRTT:   s.LatestRTT,
		RTTVariance: s.MeanDeviation,

		PacketsAcked:  s.PacketsAcked,
		BytesInFlight: s.BytesInFlight,
		Counters: internet.QuicCounters{
			PacketsSent:        s.PacketsSent,
			BytesSent:          s.BytesSent,
			PacketsReceived:    s.PacketsReceived,
			BytesReceived:      s.BytesReceived,
			PacketsLost:        s.PacketsLost,
			BytesLost:          s.BytesLost,
			SpuriousLosses:     s.SpuriousLosses,
			PTOCount:           s.PTOCount,
			CopiesSent:         s.CopiesSent,
			CopyBytesSent:      s.CopyBytesSent,
			CopiesSkipped:      s.CopiesSkipped,
			DuplicatesReceived: s.DuplicatesReceived,
		},
	}
}

func (c conn) Done() bool {
	select {
	case <-c.c.Context().Done():
		return true
	default:
		return false
	}
}

func (c conn) RemoteAddr() net.Addr { return c.c.RemoteAddr() }
