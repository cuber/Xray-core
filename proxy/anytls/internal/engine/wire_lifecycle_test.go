package anytls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

type lifecycleWireFrame struct {
	command byte
	id      uint32
	payload []byte
}

type lifecycleWireGate struct {
	net.Conn
	armed            atomic.Bool
	lastFIN          atomic.Uint32
	entered, release chan struct{}
	once             sync.Once
}

func (c *lifecycleWireGate) Write(p []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
	}
	return c.Conn.Write(p)
}

func (c *lifecycleWireGate) unblock() { c.once.Do(func() { close(c.release) }) }

func lifecycleWireWait(t *testing.T, name string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not join", name)
	}
}

func lifecycleWireUntil(t *testing.T, name string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatalf("%s barrier not reached", name)
		default:
			runtime.Gosched()
		}
	}
}

// Begin at the authenticated session boundary. Settings, padding, SYN, PSH and
// FIN are the real engine's bytes; this deliberately does not retest TLS/auth.
func lifecycleWireSession(t *testing.T, cancelable bool) (*session, net.Conn, *lifecycleWireGate, *[]lifecycleWireFrame, <-chan struct{}) {
	t.Helper()
	c, err := NewClient(ClientOptions{Password: "wire-only", CancelableWrites: cancelable})
	if err != nil {
		t.Fatal(err)
	}
	local, peer := net.Pipe()
	gate := &lifecycleWireGate{Conn: local, entered: make(chan struct{}), release: make(chan struct{})}
	s := newClientSession(c, gate)
	c.sessions[s] = struct{}{}
	var frames []lifecycleWireFrame
	recorded := make(chan struct{})
	go func() {
		defer close(recorded)
		for {
			var header [frameOverhead]byte
			if _, err := io.ReadFull(peer, header[:]); err != nil {
				return
			}
			frame := lifecycleWireFrame{command: header[0], id: binary.BigEndian.Uint32(header[1:5]), payload: make([]byte, binary.BigEndian.Uint16(header[5:]))}
			// A complete header already puts the stream command on the wire,
			// even if cancellation cuts its payload short.
			frames = append(frames, frame)
			if _, err := io.ReadFull(peer, frame.payload); err != nil {
				return
			}
			if frame.command == commandFIN {
				gate.lastFIN.Store(frame.id)
			}
		}
	}()
	t.Cleanup(func() {
		gate.unblock()
		peer.Close()
		c.Close()
		lifecycleWireWait(t, "wire recorder", recorded)
	})
	return s, peer, gate, &frames, recorded
}

func TestSessionWireSYNPrecedesPSHAndFIN(t *testing.T) {
	for _, cancelable := range []bool{false, true} {
		name := "reference-writes"
		if cancelable {
			name = "cancelable-writes"
		}
		t.Run(name, func(t *testing.T) {
			s, peer, gate, frames, recorded := lifecycleWireSession(t, cancelable)
			open := func() *stream {
				st, err := s.openStream(M.ParseSocksaddr("wire.test:443"))
				if err != nil {
					t.Fatal(err)
				}
				return st
			}
			// A completed stream makes the oracle non-vacuous: both application
			// PSH and FIN must be present, not merely absent after cancellation.
			first := open()
			if _, err := first.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			unused := open()
			if err := unused.Close(); err != nil {
				t.Fatal(err)
			}
			racing := open()
			if cancelable {
				// Native cancellation closes a partially written physical session.
				// Park its data write after admission, so Close itself owns and
				// joins finishStream rather than spawning deferred handshake work.
				if err := racing.handshake(&racing.writeDeadline); err != nil {
					t.Fatal(err)
				}
			}
			gate.armed.Store(true)
			writerDone := make(chan struct{})
			go func() { defer close(writerDone); racing.Write([]byte("racing")) }()
			t.Cleanup(func() {
				gate.unblock()
				peer.Close()
				s.Close()
				lifecycleWireWait(t, "racing writer cleanup", writerDone)
			})
			lifecycleWireWait(t, "transport write", gate.entered)
			closeDone := make(chan struct{})
			go func() { defer close(closeDone); racing.Close() }()
			t.Cleanup(func() {
				gate.unblock()
				peer.Close()
				s.Close()
				lifecycleWireWait(t, "stream Close cleanup", closeDone)
			})
			lifecycleWireWait(t, "Close started", racing.done)
			gate.unblock()
			lifecycleWireWait(t, "racing writer", writerDone)
			lifecycleWireWait(t, "racing Close", closeDone)
			if !cancelable {
				// FIN proves the deferred finish goroutine actually ran; the
				// handshake lock below then joins its remaining release work.
				lifecycleWireUntil(t, "deferred FIN receipt", func() bool { return gate.lastFIN.Load() == racing.id })
			}
			lifecycleWireUntil(t, "deferred stream finish", func() bool {
				if s.hasStreams() || !racing.handshakeAccess.TryLock() {
					return false
				}
				racing.handshakeAccess.Unlock()
				return true
			})
			s.Close()
			peer.Close()
			lifecycleWireWait(t, "wire receipts", recorded)
			seen := map[uint32]bool{}
			psh, fin := 0, 0
			payloadReceived := false
			for _, frame := range *frames {
				if frame.id == 0 {
					continue
				}
				if frame.id == unused.id {
					t.Fatal("unused stream emitted a frame")
				}
				if frame.command == commandSYN {
					if seen[frame.id] {
						t.Fatal("duplicate SYN", frame.id)
					}
					seen[frame.id] = true
				} else if !seen[frame.id] {
					t.Fatalf("command=%d before SYN for stream=%d", frame.command, frame.id)
				}
				if frame.id == first.id {
					if frame.command == commandPSH {
						psh++
						payloadReceived = payloadReceived || bytes.Equal(frame.payload, []byte("payload"))
					}
					if frame.command == commandFIN {
						fin++
					}
				}
			}
			if !seen[first.id] || psh != 2 || fin != 1 || !payloadReceived {
				t.Fatalf("completed stream: SYN=%v PSH=%d FIN=%d", seen[first.id], psh, fin)
			}
			if !seen[racing.id] {
				t.Fatal("racing stream SYN never reached wire")
			}
		})
	}
}

func TestSessionWirePeerFINWakesParkedReader(t *testing.T) {
	for _, api := range []string{"Read", "ReadBuffer", "WaitReadBuffer"} {
		t.Run(api, func(t *testing.T) {
			s, peer, _, _, _ := lifecycleWireSession(t, true)
			st, err := s.openStream(M.ParseSocksaddr("wire.test:443"))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.handshake(&st.writeDeadline); err != nil {
				t.Fatal(err)
			}
			loopDone := make(chan struct{})
			go func() { defer close(loopDone); defer s.Close(); s.readLoop() }()
			readDone := make(chan struct{})
			var readErr error
			// Seed an ordinary buffered wakeup. Only waitReadLocked consumes
			// this token: observing consumption proves Read reached its waiting
			// path without relying on a goroutine-start signal or a sleep.
			st.readSignal <- struct{}{}
			go func() {
				defer close(readDone)
				switch api {
				case "Read":
					_, readErr = st.Read(make([]byte, 1))
				case "ReadBuffer":
					b := buf.NewSize(16)
					defer b.Release()
					readErr = st.ReadBuffer(b)
				case "WaitReadBuffer":
					b, err := st.WaitReadBuffer()
					readErr = err
					if b != nil {
						b.Release()
					}
				}
			}()
			t.Cleanup(func() {
				peer.Close()
				s.Close()
				lifecycleWireWait(t, "session read loop", loopDone)
				lifecycleWireWait(t, "stream reader cleanup", readDone)
			})
			lifecycleWireUntil(t, "parked read", func() bool { return len(st.readSignal) == 0 })
			select {
			case <-readDone:
				t.Fatal("reader returned before peer FIN", readErr)
			default:
			}
			var frame [frameOverhead]byte
			putFrameHeader(frame[:], commandFIN, st.id, 0)
			if err := peer.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := peer.Write(frame[:]); err != nil {
				t.Fatal(err)
			}
			lifecycleWireWait(t, "peer-FIN reader wakeup", readDone)
			if !errors.Is(readErr, io.EOF) {
				t.Fatalf("peer FIN returned %v, want EOF (not timeout/local close)", readErr)
			}
			if s.IsClosed() || s.hasStreams() {
				t.Fatal("peer FIN must remove only its stream, not close the session")
			}
		})
	}
}
