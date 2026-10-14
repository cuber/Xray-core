package buf_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

type boundarySink struct {
	bytes.Buffer
	chunk, failAfter int
	failure          error
	calls            int
}

func (s *boundarySink) Write(p []byte) (int, error) {
	s.calls++
	n := len(p)
	if s.chunk > 0 && n > s.chunk {
		n = s.chunk
	}
	if s.failure != nil && s.Len()+n >= s.failAfter {
		n = s.failAfter - s.Len()
		if n < 0 {
			n = 0
		}
		s.Buffer.Write(p[:n])
		return n, s.failure
	}
	return s.Buffer.Write(p[:n])
}

type releasingSink struct {
	data     bytes.Buffer
	released int
}

func (s *releasingSink) Bytes() []byte { return s.data.Bytes() }
func (s *releasingSink) Len() int      { return s.data.Len() }

func (s *releasingSink) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for _, b := range mb {
		s.data.Write(b.Bytes())
		s.released++
	}
	buf.ReleaseMulti(mb)
	return nil
}

func boundaryPayload(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte((i*37 + 11) % 251)
	}
	return p
}

func TestBufferedWriterPartialCapacityBoundaries(t *testing.T) {
	for _, used := range []int{0, 1, buf.Size / 2, buf.Size - 1} {
		remaining := buf.Size - used
		for _, size := range []int{0, remaining - 1, remaining, remaining + 1, 2*buf.Size + 17} {
			for _, multi := range []bool{false, true} {
				t.Run(fmt.Sprintf("used=%d/write=%d/multi=%v", used, size, multi), func(t *testing.T) {
					sink := &boundarySink{chunk: 7}
					mbSink := new(releasingSink)
					var target buf.Writer = &buf.BufferToBytesWriter{Writer: sink}
					if multi {
						target = mbSink
					}
					w := buf.NewBufferedWriter(target)
					t.Cleanup(func() { w.Close() })
					prefix, payload := bytes.Repeat([]byte{0xa7}, used), boundaryPayload(size)
					if n, err := w.Write(prefix); n != used || err != nil {
						t.Fatalf("prefix n=%d err=%v", n, err)
					}
					if n, err := w.Write(payload); n != size || err != nil {
						t.Fatalf("payload n=%d want=%d err=%v", n, size, err)
					}
					if err := w.Flush(); err != nil {
						t.Fatal(err)
					}
					got := sink.Bytes()
					if multi {
						got = mbSink.Bytes()
					}
					want := append(bytes.Clone(prefix), payload...)
					if !bytes.Equal(got, want) {
						t.Fatalf("wire bytes differ: got=%d want=%d", len(got), len(want))
					}
					if multi && mbSink.released != (len(want)+buf.Size-1)/buf.Size {
						t.Fatalf("owned buffers released=%d", mbSink.released)
					}
					if err := w.Flush(); err != nil {
						t.Fatal(err)
					}
					if multi && mbSink.Len() != len(want) || !multi && sink.Len() != len(want) {
						t.Fatal("second flush duplicated bytes")
					}
				})
			}
		}
	}
}

func TestBufferedWriterDownstreamErrors(t *testing.T) {
	for _, failure := range []error{io.ErrShortWrite, io.ErrClosedPipe} {
		for _, used := range []int{0, 1, buf.Size - 1} {
			for _, accepted := range []int{0, 1, buf.Size - 1, buf.Size} {
				t.Run(fmt.Sprintf("%v/used=%d/accepted=%d", failure, used, accepted), func(t *testing.T) {
					sink := &boundarySink{failAfter: accepted, failure: failure, chunk: 13}
					w := buf.NewBufferedWriter(&buf.BufferToBytesWriter{Writer: sink})
					prefix := bytes.Repeat([]byte{0x53}, used)
					if n, err := w.Write(prefix); n != used || err != nil {
						t.Fatalf("prefix n=%d err=%v", n, err)
					}
					payload := boundaryPayload(buf.Size - used + 1)
					n, err := w.Write(payload)
					// n is bytes accepted into the buffer, not the sink's partial
					// delivery count. A terminal flush error does not promise retry.
					if n != buf.Size-used || !errors.Is(err, failure) {
						t.Fatalf("n=%d err=%v", n, err)
					}
					want := append(bytes.Clone(prefix), payload...)[:accepted]
					if !bytes.Equal(sink.Bytes(), want) {
						t.Fatalf("partial delivery bytes=%d want=%d", sink.Len(), accepted)
					}
					calls := sink.calls
					if err := w.Flush(); err != nil || sink.calls != calls {
						t.Fatalf("failed flush retried or duplicated: %v", err)
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestBufferedWriterFlushAndUnbufferedShortWrite(t *testing.T) {
	for _, disable := range []bool{false, true} {
		sink := &boundarySink{failure: io.ErrShortWrite, failAfter: 3}
		w := buf.NewBufferedWriter(&buf.BufferToBytesWriter{Writer: sink})
		if disable {
			if err := w.SetBuffered(false); err != nil {
				t.Fatal(err)
			}
		}
		payload := boundaryPayload(17)
		n, err := w.Write(payload)
		if disable {
			if n != 3 || !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("unbuffered n=%d err=%v", n, err)
			}
		} else {
			if n != 17 || err != nil {
				t.Fatalf("buffered n=%d err=%v", n, err)
			}
			if err := w.SetBuffered(false); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("flush error=%v", err)
			}
		}
		if !bytes.Equal(sink.Bytes(), payload[:3]) {
			t.Fatal("short write emitted wrong bytes")
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
