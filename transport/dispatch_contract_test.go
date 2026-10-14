package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport/pipe"
)

func awaitContract(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("operation did not finish within one second")
	}
}

func TestDispatchContractRunnerExit64KiB(t *testing.T) {
	links := make(chan *Link, 1)
	conn := NewDispatchConn(context.Background(), []pipe.Option{pipe.WithSizeLimit(65536)}, DispatchConnOutputStream, func(_ context.Context, l *Link) { links <- l })
	defer conn.Close()
	link := <-links
	payload := make([]byte, 65536)
	for i := range payload {
		payload[i] = byte(i*31 + 7)
	}
	done := make(chan error, 1)
	go func() { done <- buf.Copy(link.Reader, link.Writer) }()
	exchange := make(chan error, 1)
	go func() {
		if _, err := conn.Write(payload); err != nil {
			exchange <- err
			return
		}
		got := make([]byte, len(payload))
		_, err := io.ReadFull(conn, got)
		if err == nil && !bytes.Equal(payload, got) {
			err = io.ErrUnexpectedEOF
		}
		exchange <- err
	}()
	awaitContract(t, exchange)
	t.Logf("payload=%d sha256=%x", len(payload), sha256.Sum256(payload))
	conn.Close()
	awaitContract(t, done)
	conn.Close()
}

func TestDispatchContractConcurrentCloseInterrupt(t *testing.T) {
	for _, output := range []DispatchConnOutput{DispatchConnOutputStream, DispatchConnOutputPacket} {
		for round := 0; round < 100; round++ {
			links := make(chan *Link, 1)
			conn := NewDispatchConn(context.Background(), nil, output, func(_ context.Context, l *Link) { links <- l })
			link := <-links
			mb, err := link.Reader.(buf.TimeoutReader).ReadMultiBufferTimeout(time.Millisecond)
			buf.ReleaseMulti(mb)
			if !errors.Is(err, buf.ErrReadTimeout) {
				t.Fatalf("timeout read error = %v, want buf.ErrReadTimeout", err)
			}
			entered := make(chan struct{})
			read := make(chan error, 1)
			go func() {
				close(entered)
				mb, err := link.Reader.ReadMultiBuffer()
				buf.ReleaseMulti(mb)
				if err == nil {
					read <- io.ErrUnexpectedEOF
				} else {
					read <- nil
				}
			}()
			<-entered
			response := make(chan error, 1)
			go func() {
				var b [1]byte
				_, err := conn.Read(b[:])
				if err == nil {
					response <- io.ErrUnexpectedEOF
				} else {
					response <- nil
				}
			}()
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if i%2 == 0 {
						conn.Close()
					} else {
						common.Interrupt(link.Reader)
					}
				}(i)
			}
			joined := make(chan error, 1)
			go func() { wg.Wait(); joined <- nil }()
			awaitContract(t, joined)
			awaitContract(t, read)
			awaitContract(t, response)
			conn.Close()
		}
	}
}

func TestDispatchContractInterruptUnblocksWaitingRead(t *testing.T) {
	links := make(chan *Link, 1)
	conn := NewDispatchConn(context.Background(), nil, DispatchConnOutputStream, func(_ context.Context, link *Link) { links <- link })
	defer conn.Close()
	link := <-links
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(entered)
		mb, err := link.Reader.ReadMultiBuffer()
		buf.ReleaseMulti(mb)
		if err == nil {
			finished <- io.ErrUnexpectedEOF
		} else {
			finished <- nil
		}
	}()
	<-entered
	common.Interrupt(link.Reader)
	awaitContract(t, finished)
}

func TestDispatchContractPacketResponseBoundaries(t *testing.T) {
	links := make(chan *Link, 1)
	conn := NewDispatchConn(context.Background(), nil, DispatchConnOutputPacket, func(_ context.Context, l *Link) { links <- l })
	defer conn.Close()
	link := <-links
	var packets buf.MultiBuffer
	for i, n := range []int{1, 17, 1200} {
		b := buf.New()
		b.Write(bytes.Repeat([]byte{byte(i + 1)}, n))
		packets = append(packets, b)
	}
	if err := link.Writer.WriteMultiBuffer(packets); err != nil {
		t.Fatal(err)
	}
	for i, n := range []int{1, 17, 1200} {
		got := make([]byte, 2048)
		count, err := conn.Read(got)
		if err != nil || count != n || !bytes.Equal(got[:count], bytes.Repeat([]byte{byte(i + 1)}, n)) {
			t.Fatalf("packet %d: length=%d err=%v", i, count, err)
		}
	}
	t.Log("response packet lengths: 1,17,1200; request side remains a stream")
}
