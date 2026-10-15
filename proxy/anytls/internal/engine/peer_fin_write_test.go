package anytls

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

type peerFINWriteBarrier struct {
	net.Conn
	first                     atomic.Bool
	delivered, resume, closed chan struct{}
	once                      sync.Once
}

func (c *peerFINWriteBarrier) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && c.first.CompareAndSwap(false, true) {
		close(c.delivered)
		select {
		case <-c.resume:
		case <-c.closed:
			return n, net.ErrClosed
		}
	}
	return n, err
}

func (c *peerFINWriteBarrier) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestPeerFINDoesNotCancelDeliveredServerWrite(t *testing.T) {
	for _, mode := range []string{"peer-fin", "local-close-after-peer-fin", "deadline-after-peer-fin", "session-close-after-peer-fin"} {
		t.Run(mode, func(t *testing.T) {
			a, peer := net.Pipe()
			conn := &peerFINWriteBarrier{Conn: a, delivered: make(chan struct{}), resume: make(chan struct{}), closed: make(chan struct{})}
			s := &session{conn: conn, reader: bufio.NewReader(conn), writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
			first, other := newStream(1, s), newStream(2, s)
			first.handshakeDone.Store(true)
			if mode == "deadline-after-peer-fin" || mode == "peer-fin" {
				first.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			}
			other.handshakeDone.Store(true)
			s.streams[1], s.streams[2] = first, other
			loopDone, writeDone := make(chan struct{}), make(chan error, 1)
			go func() { defer close(loopDone); s.readLoop() }()
			go func() { _, err := first.Write([]byte("reply")); writeDone <- err }()
			t.Cleanup(func() { peer.Close(); s.Close(); <-loopDone })
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			wire := make([]byte, frameOverhead+len("reply"))
			if _, err := io.ReadFull(peer, wire); err != nil || !bytes.Equal(wire[frameOverhead:], []byte("reply")) {
				t.Fatalf("reply: %q %v", wire, err)
			}
			<-conn.delivered
			var fin [frameOverhead]byte
			putFrameHeader(fin[:], commandFIN, first.id, 0)
			if _, err := peer.Write(fin[:]); err != nil {
				t.Fatal(err)
			}
			<-first.done
			if mode != "peer-fin" {
				if mode == "local-close-after-peer-fin" {
					first.Close()
				} else if mode == "session-close-after-peer-fin" {
					s.Close()
				}
				select {
				case err := <-writeDone:
					if err == nil || !s.IsClosed() {
						t.Fatal("termination did not abort blocked write")
					}
				case <-time.After(time.Second):
					t.Fatal("termination after FIN left writer blocked")
				}
				return
			}
			select {
			case <-conn.closed:
				t.Fatal("peer FIN aborted the physical session with a completed reply")
			case <-time.After(50 * time.Millisecond):
			}
			close(conn.resume)
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			go func() { _, err := other.Write([]byte("alive")); writeDone <- err }()
			if _, err := io.ReadFull(peer, wire); err != nil || !bytes.Equal(wire[frameOverhead:], []byte("alive")) {
				t.Fatalf("unrelated stream lost: %q %v", wire, err)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			select {
			case <-first.writeDeadline.Wait():
				t.Fatal("completed peer-FIN write retained its deadline timer")
			case <-time.After(220 * time.Millisecond):
			}
		})
	}
}

func TestPeerFINIdleWriteDeadlineReleased(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	s := &session{conn: a, writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
	stream := newStream(1, s)
	s.streams[1] = stream
	stream.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	stream.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	stream.closeByPeer()
	select {
	case <-stream.writeDeadline.Wait():
		t.Fatal("idle peer-FIN stream retained its deadline timer")
	case <-stream.readDeadline.Wait():
		t.Fatal("idle peer-FIN stream retained its read deadline timer")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLocalCloseReleasesConcurrentWriteDeadline(t *testing.T) {
	stream := newStream(1, nil)
	// A setter may acquire the deadline lock between cancellation and done.
	stream.cancelWrites()
	if err := stream.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	stream.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	stream.closeLocally(net.ErrClosed)
	select {
	case <-stream.writeDeadline.Wait():
		t.Fatal("local close retained a concurrently installed write deadline")
	case <-stream.readDeadline.Wait():
		t.Fatal("local close retained a concurrently installed read deadline")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPeerFINReadHandshakeDeadline(t *testing.T) {
	for _, reader := range []string{"Read", "ReadBuffer", "WaitReadBuffer", "Write", "WriteBuffer"} {
		for _, complete := range []bool{false, true} {
			t.Run(reader+map[bool]string{false: "/deadline", true: "/complete"}[complete], func(t *testing.T) {
				a, peer := net.Pipe()
				conn := &peerFINWriteBarrier{Conn: a, delivered: make(chan struct{}), resume: make(chan struct{}), closed: make(chan struct{})}
				s := &session{isClient: true, conn: conn, reader: bufio.NewReader(conn), writeAccess: make(chan struct{}, 1), done: make(chan struct{}), streams: make(map[uint32]*stream)}
				stream := newStream(1, s)
				stream.destination = M.ParseSocksaddr("target.test:80")
				s.streams[1] = stream
				stream.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				limit := &stream.readDeadline
				if reader == "Write" || reader == "WriteBuffer" {
					stream.SetReadDeadline(time.Time{})
					stream.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
					limit = &stream.writeDeadline
				}
				loop, read := make(chan struct{}), make(chan error, 1)
				go func() { defer close(loop); s.readLoop() }()
				go func() {
					var err error
					switch reader {
					case "Write":
						_, err = stream.Write([]byte("request"))
					case "WriteBuffer":
						b := buf.NewSize(16)
						b.WriteString("request")
						err = stream.WriteBuffer(b)
					case "Read":
						_, err = stream.Read(make([]byte, 1))
					case "ReadBuffer":
						b := buf.NewSize(1)
						defer b.Release()
						err = stream.ReadBuffer(b)
					case "WaitReadBuffer":
						b, e := stream.WaitReadBuffer()
						err = e
						if b != nil {
							b.Release()
						}
					}
					read <- err
				}()
				t.Cleanup(func() { peer.Close(); s.Close(); <-loop })
				peer.SetDeadline(time.Now().Add(2 * time.Second))
				var header [frameOverhead]byte
				if _, err := io.ReadFull(peer, header[:]); err != nil || header[0] != commandSYN {
					t.Fatalf("SYN=%v err=%v", header, err)
				}
				<-conn.delivered
				putFrameHeader(header[:], commandFIN, stream.id, 0)
				if _, err := peer.Write(header[:]); err != nil {
					t.Fatal(err)
				}
				<-stream.done
				if complete {
					close(conn.resume)
					if _, err := io.ReadFull(peer, header[:]); err != nil {
						t.Fatal(err)
					}
					if _, err := io.CopyN(io.Discard, peer, int64(binary.BigEndian.Uint16(header[5:]))); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case err := <-read:
					if err == nil {
						t.Fatal("closed handshake unexpectedly read data")
					}
				case <-time.After(time.Second):
					t.Fatal("peer FIN erased active read-handshake deadline")
				}
				if complete {
					if s.IsClosed() {
						t.Fatal("completed handshake aborted session")
					}
					select {
					case <-limit.Wait():
						t.Fatal("completed handshake retained read timer")
					case <-time.After(220 * time.Millisecond):
					}
				} else if !s.IsClosed() {
					t.Fatal("read deadline failed to abort stalled handshake")
				}
			})
		}
	}
}
