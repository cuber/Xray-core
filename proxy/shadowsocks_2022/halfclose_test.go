package shadowsocks_2022

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type terminalReader struct {
	buf.Reader
	once sync.Once
	done chan struct{}
}

func (r *terminalReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if err != nil {
		r.once.Do(func() { close(r.done) })
	}
	return mb, err
}

func TestTransportTCPHalfClosePolicy(t *testing.T) {
	for _, direction := range []string{"uplink-eof", "downlink-eof", "idle"} {
		t.Run(direction, func(t *testing.T) {
			upRead, upWrite := pipe.New()
			downRead, downWrite := pipe.New()
			toBackend, backendWrite := pipe.New()
			toClient, clientWrite := pipe.New()
			up := &terminalReader{Reader: upRead, done: make(chan struct{})}
			down := &terminalReader{Reader: downRead, done: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer func() {
				upRead.Interrupt()
				downRead.Interrupt()
				toBackend.Interrupt()
				toClient.Interrupt()
				for _, done := range []chan struct{}{up.done, down.done} {
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Error("copy reader did not retire after owner shutdown")
					}
				}
			}()
			p := policy.SessionDefault()
			p.Timeouts.ConnectionIdle = 10 * time.Second
			p.Timeouts.UplinkOnly = 10 * time.Second
			p.Timeouts.DownlinkOnly = 10 * time.Second
			switch direction {
			case "uplink-eof":
				p.Timeouts.DownlinkOnly = 200 * time.Millisecond
			case "downlink-eof":
				p.Timeouts.UplinkOnly = 200 * time.Millisecond
			case "idle":
				p.Timeouts.ConnectionIdle = 200 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				done <- TransportTCP(ctx, p, up, clientWrite, &transport.Link{Reader: down, Writer: backendWrite})
			}()
			if direction != "idle" {
				closed, stillOpen, receive := upWrite, downWrite, toClient
				if direction == "downlink-eof" {
					closed, stillOpen, receive = downWrite, upWrite, toBackend
				}
				if err := closed.Close(); err != nil {
					t.Fatal(err)
				}
				if err := stillOpen.WriteMultiBuffer(buf.MergeBytes(nil, []byte("remaining-direction"))); err != nil {
					t.Fatal(err)
				}
				mb, err := receive.ReadMultiBufferTimeout(time.Second)
				if err != nil {
					t.Fatal(err)
				}
				got := mb.String()
				buf.ReleaseMulti(mb)
				if got != "remaining-direction" {
					t.Fatalf("remaining direction corrupted: %q", got)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("inactivity did not terminate through cancellation")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("selected inactivity policy was not applied")
			}
		})
	}
}
