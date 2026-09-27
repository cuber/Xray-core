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
// pipes. The request reader passed to the handler borrows the real pipe reader:
// it preserves buf.TimeoutReader and forwards common.Interruptible so
// timeout-based teardown in singbridge/PipeConnWrapper can abort the
// underlying pipe. Pipe ownership stays with the returned connection —
// ConnectionOnClose tears the pipes down when the caller closes the conn.
// The runner's exit is NOT a signal to tear down pending writes: self-loop /
// fast-path runners can return before the caller has written the first byte,
// and preemptively closing the response pipe there delivers a zero-payload
// FIN to the caller before any data flowed.
func NewDispatchConn(ctx context.Context, opts []pipe.Option, output DispatchConnOutput, run func(context.Context, *Link)) xnet.Conn {
	requestReader, requestWriter := pipe.New(opts...)
	responseReader, responseWriter := pipe.New(opts...)

	go run(ctx, &Link{
		Reader: &borrowedReader{Reader: requestReader},
		Writer: responseWriter,
	})

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

// Interrupt forwards to the underlying reader so callers that use
// common.Interrupt(linkReader) (e.g. singbridge/PipeConnWrapper on read
// timeout) actually abort the pipe instead of no-oping.
func (r *borrowedReader) Interrupt() {
	common.Interrupt(r.Reader)
}
