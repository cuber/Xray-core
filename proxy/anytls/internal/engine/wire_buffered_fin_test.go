package anytls

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

func TestSessionWireBufferedPSHSurvivesPeerFIN(t *testing.T) {
	s, peer, _, _, _ := lifecycleWireSession(t, true)
	st, err := s.openStream(M.ParseSocksaddr("buffered.test:443"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.handshake(&st.writeDeadline); err != nil {
		t.Fatal(err)
	}
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		defer s.Close()
		s.readLoop()
	}()
	t.Cleanup(func() {
		peer.Close()
		s.Close()
		lifecycleWireWait(t, "buffered-FIN session read loop", loopDone)
	})

	// One PSH fits in readPending, allowing the real readLoop to consume FIN
	// before any application read. Multiple unread PSHs would hit backpressure.
	payload := make([]byte, 40000)
	for i := range payload {
		payload[i] = byte(i*31 + i/251)
	}
	wire := make([]byte, frameOverhead+len(payload)+frameOverhead)
	putFrameHeader(wire[:frameOverhead], commandPSH, st.id, len(payload))
	copy(wire[frameOverhead:], payload)
	putFrameHeader(wire[frameOverhead+len(payload):], commandFIN, st.id, 0)
	if err := peer.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := peer.Write(wire); err != nil || n != len(wire) {
		t.Fatalf("peer PSH/FIN write=%d want=%d err=%v", n, len(wire), err)
	}
	// Transport Write completion alone does not prove FIN handling. readEnd is
	// published by closeByPeer/endRead after the stream is removed from session.
	lifecycleWireUntil(t, "FIN consumed with unread payload", func() bool {
		st.readAccess.Lock()
		defer st.readAccess.Unlock()
		return st.readEnd
	})
	st.readAccess.Lock()
	buffered := st.readPending != nil && st.readPending.Len() == len(payload) && bytes.Equal(st.readPending.Bytes(), payload)
	noCache := st.readCache == nil
	endErr := st.readErr
	st.readAccess.Unlock()
	if !buffered || !noCache || !errors.Is(endErr, io.EOF) || s.IsClosed() || s.hasStreams() {
		t.Fatalf("FIN state: full pending=%v no cache=%v end=%v session closed=%v streams=%v", buffered, noCache, endErr, s.IsClosed(), s.hasStreams())
	}

	readerDone := make(chan struct{})
	var got []byte
	var readerErr error
	reads := 0
	go func() {
		defer close(readerDone)
		chunk := make([]byte, 512)
		for len(got) < len(payload) {
			n, err := st.Read(chunk)
			if err != nil || n == 0 {
				readerErr = fmt.Errorf("payload read at %d: n=%d err=%v", len(got), n, err)
				return
			}
			got = append(got, chunk[:n]...)
			reads++
			if reads == 1 {
				st.readAccess.Lock()
				cached := st.readPending == nil && st.readCache != nil && st.readCache.Len() == len(payload)-n
				st.readAccess.Unlock()
				if !cached {
					readerErr = fmt.Errorf("first partial read lost unread cache")
					return
				}
			}
		}
		if n, err := st.Read(chunk); n != 0 || !errors.Is(err, io.EOF) {
			readerErr = fmt.Errorf("after payload: n=%d err=%v, want 0/EOF", n, err)
		}
	}()
	t.Cleanup(func() {
		st.Close()
		s.Close()
		peer.Close()
		lifecycleWireWait(t, "buffered-FIN reader cleanup", readerDone)
	})
	lifecycleWireWait(t, "buffered-FIN payload reader", readerDone)
	if readerErr != nil || !bytes.Equal(got, payload) || reads != (len(payload)+511)/512 {
		t.Fatalf("payload bytes=%d reads=%d err=%v exact=%v", len(got), reads, readerErr, bytes.Equal(got, payload))
	}
	st.readAccess.Lock()
	empty := st.readPending == nil && st.readCache == nil
	st.readAccess.Unlock()
	if !empty || s.IsClosed() {
		t.Fatal("drained FIN retained buffers or closed the physical session")
	}
	t.Logf("FIN consumed before first Read; %d bytes preserved through pending/cache, %d reads <=512B, then EOF; session remains open", len(got), reads)
}
