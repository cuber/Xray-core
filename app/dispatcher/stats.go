package dispatcher

import (
	"time"

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

type DomainTrafficReader struct {
	Recorder stats.DomainTrafficManager
	Outbound []*session.Outbound
	Reader   buf.Reader
}

func (r *DomainTrafficReader) record(mb buf.MultiBuffer) {
	bytes := uint64(mb.Len())
	if bytes == 0 {
		return
	}
	r.Recorder.RecordDomainTraffic(outboundDomain(r.Outbound), bytes, 0)
}

func (r *DomainTrafficReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	r.record(mb)
	return mb, err
}

func (r *DomainTrafficReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	timeoutReader, ok := r.Reader.(buf.TimeoutReader)
	if !ok {
		return nil, buf.ErrNotTimeoutReader
	}
	mb, err := timeoutReader.ReadMultiBufferTimeout(timeout)
	r.record(mb)
	return mb, err
}

func (r *DomainTrafficReader) Close() error { return common.Close(r.Reader) }

func (r *DomainTrafficReader) Interrupt() { common.Interrupt(r.Reader) }

func (w *DomainTrafficWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	bytes := uint64(mb.Len())
	domain := outboundDomain(w.Outbound)
	if w.Uplink {
		w.Recorder.RecordDomainTraffic(domain, bytes, 0)
	} else {
		w.Recorder.RecordDomainTraffic(domain, 0, bytes)
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func outboundDomain(outbounds []*session.Outbound) string {
	if len(outbounds) == 0 {
		return "unknown"
	}
	outbound := outbounds[len(outbounds)-1]
	for _, target := range []net.Destination{outbound.Target, outbound.RouteTarget, outbound.OriginalTarget} {
		if target.Address != nil && target.Address.Family().IsDomain() && target.Address.Domain() != "" {
			return target.Address.Domain()
		}
	}
	return "unknown"
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
