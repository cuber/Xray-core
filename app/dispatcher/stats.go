package dispatcher

import (
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/stats"
)

type DomainTrafficWriter struct {
	Recorder stats.DomainTrafficManager
	Outbound []*session.Outbound
	Uplink   bool
	Writer   buf.Writer
}

func (w *DomainTrafficWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	bytes := uint64(mb.Len())
	domain := "unknown"
	if len(w.Outbound) > 0 {
		outbound := w.Outbound[len(w.Outbound)-1]
		for _, target := range []net.Destination{outbound.Target, outbound.RouteTarget, outbound.OriginalTarget} {
			if target.Address != nil && target.Address.Family().IsDomain() && target.Address.Domain() != "" {
				domain = target.Address.Domain()
				break
			}
		}
	}
	if w.Uplink {
		w.Recorder.RecordDomainTraffic(domain, bytes, 0)
	} else {
		w.Recorder.RecordDomainTraffic(domain, 0, bytes)
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *DomainTrafficWriter) Close() error { return common.Close(w.Writer) }

func (w *DomainTrafficWriter) Interrupt() { common.Interrupt(w.Writer) }

type SizeStatWriter struct {
	Counter stats.Counter
	Writer  buf.Writer
}

func (w *SizeStatWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.Counter.Add(int64(mb.Len()))
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *SizeStatWriter) Close() error {
	return common.Close(w.Writer)
}

func (w *SizeStatWriter) Interrupt() {
	common.Interrupt(w.Writer)
}
