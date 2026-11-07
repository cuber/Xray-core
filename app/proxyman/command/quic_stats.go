package command

import (
	"context"
	"sort"
	"time"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// quicTransports are the transports whose handlers GetQuicStats reports (spec021 R3).
var quicTransports = map[string]bool{"hysteria": true}

// streamSettingsProvider is a handler that holds the stream settings its connections are built from.
type streamSettingsProvider interface {
	StreamSettings() *internet.MemoryStreamConfig
}

// GetQuicStats reports the QUIC connections of every inbound and outbound whose stream
// settings build them, or of the handlers with the given tag (spec021 R3). It is read only.
func (s *handlerServer) GetQuicStats(ctx context.Context, request *GetQuicStatsRequest) (*GetQuicStatsResponse, error) {
	var inbounds []inbound.Handler
	var outbounds []outbound.Handler
	if request.Tag != "" {
		if h, err := s.ihm.GetHandler(ctx, request.Tag); err == nil {
			inbounds = append(inbounds, h)
		}
		if h := s.ohm.GetHandler(request.Tag); h != nil {
			outbounds = append(outbounds, h)
		}
	} else {
		inbounds = s.ihm.ListHandlers(ctx)
		outbounds = s.ohm.ListHandlers(ctx)
	}
	response := &GetQuicStatsResponse{}
	for _, h := range inbounds {
		if stats := quicHandlerStats(h, h.Tag(), "inbound", request.Connections); stats != nil {
			response.Handlers = append(response.Handlers, stats)
		}
	}
	for _, h := range outbounds {
		if stats := quicHandlerStats(h, h.Tag(), "outbound", request.Connections); stats != nil {
			response.Handlers = append(response.Handlers, stats)
		}
	}
	if request.Tag != "" && len(response.Handlers) == 0 {
		return nil, status.Error(codes.NotFound, "no QUIC handler "+request.Tag)
	}
	sort.Slice(response.Handlers, func(i, j int) bool {
		a, b := response.Handlers[i], response.Handlers[j]
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		return a.Tag < b.Tag
	})
	return response, nil
}

func quicHandlerStats(handler any, tag, direction string, rows bool) *QuicHandlerStats {
	p, ok := handler.(streamSettingsProvider)
	if !ok {
		return nil
	}
	mss := p.StreamSettings()
	if mss == nil || !quicTransports[mss.ProtocolName] {
		return nil
	}
	report := mss.QuicStats.Report(rows)
	stats := &QuicHandlerStats{
		Tag:               tag,
		Direction:         direction,
		Transport:         mss.ProtocolName,
		Effective:         quicEffective(quicrecovery.Effective(mss.QuicParams)),
		Totals:            quicCounters(report.Totals),
		ConnectionsActive: report.ConnectionsActive,
		ConnectionsTotal:  report.ConnectionsTotal,
		Truncated:         report.Truncated,
	}
	for _, c := range report.Connections {
		stats.Connections = append(stats.Connections, &QuicConnection{
			Remote:        c.Remote,
			User:          c.User,
			AgeMs:         c.Age.Milliseconds(),
			SmoothedRttUs: c.SmoothedRTT.Microseconds(),
			MinRttUs:      c.MinRTT.Microseconds(),
			LatestRttUs:   c.LatestRTT.Microseconds(),
			RttVarianceUs: c.RTTVariance.Microseconds(),
			Counters:      quicCounters(c.Counters),
		})
	}
	return stats
}

func quicEffective(r quic.LossRecovery) *QuicEffective {
	return &QuicEffective{
		MaxAckDelayMs:         uint32(r.MaxAckDelay / time.Millisecond),
		AckElicitingThreshold: uint32(r.AckElicitingThreshold),
		InitialRttMs:          uint32(r.InitialRTT / time.Millisecond),
		PtoBackoffLimit:       uint32(r.PTOBackoffLimit),
		LossTimeThreshold:     r.LossTimeThreshold,
		LossPacketThreshold:   uint32(r.LossPacketThreshold),
		DuplicateMaxSize:      uint32(r.DuplicateMaxSize),
		DuplicateTail:         r.DuplicateTail,
		DuplicateDelayMs:      uint32(r.DuplicateDelay / time.Millisecond),
	}
}

func quicCounters(c internet.QuicCounters) *QuicCounters {
	return &QuicCounters{
		PacketsSent:        c.PacketsSent,
		BytesSent:          c.BytesSent,
		PacketsReceived:    c.PacketsReceived,
		BytesReceived:      c.BytesReceived,
		PacketsLost:        c.PacketsLost,
		BytesLost:          c.BytesLost,
		SpuriousLosses:     c.SpuriousLosses,
		PtoCount:           c.PTOCount,
		CopiesSent:         c.CopiesSent,
		CopyBytesSent:      c.CopyBytesSent,
		CopiesSkipped:      c.CopiesSkipped,
		DuplicatesReceived: c.DuplicatesReceived,
	}
}
