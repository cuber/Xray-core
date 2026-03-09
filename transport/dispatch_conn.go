package transport

import (
	"context"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/transport/pipe"
)

// DispatchConnOutput controls how the response side of a dispatch-backed
// connection is exposed to callers.
type DispatchConnOutput byte

const (
	DispatchConnOutputStream DispatchConnOutput = iota
	DispatchConnOutputPacket
)

// NewDispatchConn runs transport handler logic behind a net.Conn backed by two
// pipes. The request reader passed to the handler is borrowed: it preserves
// buf.TimeoutReader, but intentionally hides Close/Interrupt so ownership stays
// with the returned connection.
func NewDispatchConn(ctx context.Context, opts []pipe.Option, output DispatchConnOutput, run func(context.Context, *Link)) xnet.Conn {
	requestReader, requestWriter := pipe.New(opts...)
	responseReader, responseWriter := pipe.New(opts...)

	go func() {
		run(ctx, &Link{
			Reader: &borrowedReader{Reader: requestReader},
			Writer: responseWriter,
		})
		// The runner no longer owns the request side. Once it exits, fail pending
		// and future client writes promptly and close the response side.
		common.Interrupt(requestWriter)
		common.Close(responseWriter)
	}()

	readerOpt := cnc.ConnectionOutputMulti(responseReader)
	if output == DispatchConnOutputPacket {
		readerOpt = cnc.ConnectionOutputMultiUDP(responseReader)
	}

	return cnc.NewConnection(
		cnc.ConnectionInputMulti(requestWriter),
		readerOpt,
		cnc.ConnectionOnClose(common.ChainedClosable{requestWriter, responseWriter}),
	)
}

type borrowedReader struct {
	buf.Reader
}

func (r *borrowedReader) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	if timeoutReader, ok := r.Reader.(buf.TimeoutReader); ok {
		return timeoutReader.ReadMultiBufferTimeout(d)
	}
	return nil, buf.ErrNotTimeoutReader
}
