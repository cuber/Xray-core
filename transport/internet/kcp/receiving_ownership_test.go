package kcp

import (
	"bytes"
	"sync"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

func receivingTestBuffer(data []byte) *buf.Buffer {
	b := buf.New()
	b.Write(data)
	return b
}

func TestReceivingWorkerPartialReadOrder(t *testing.T) {
	w := &ReceivingWorker{window: NewReceivingWindow()}
	w.leftOver = buf.MultiBuffer{receivingTestBuffer([]byte("prefix"))}
	for i, data := range []string{"first", "second"} {
		seg := NewDataSegment()
		seg.Data().Write([]byte(data))
		w.window.Set(uint32(i), seg)
	}
	var got []byte
	for _, size := range []int{0, 2, 3, 8, 1} {
		p := make([]byte, size)
		n := w.Read(p)
		got = append(got, p[:n]...)
	}
	mb := w.ReadMultiBuffer()
	// Release must not reclaim buffers already handed to a caller.
	w.Release()
	for _, b := range mb {
		got = append(got, b.Bytes()...)
	}
	buf.ReleaseMulti(mb)
	if string(got) != "prefixfirstsecond" {
		t.Fatalf("byte order changed: %q", got)
	}
	if w.nextNumber != 2 || !w.ReadMultiBuffer().IsEmpty() {
		t.Fatal("window not drained exactly once")
	}
	for range 2 {
		w.Release()
	}
}

func TestReceivingWorkerConcurrentOwnership(t *testing.T) {
	for _, release := range []bool{false, true} {
		name := "readers"
		if release {
			name = "readers-and-release"
		}
		t.Run(name, func(t *testing.T) {
			for range 200 {
				want := make([]byte, 256)
				for i := range want {
					want[i] = byte(i)
				}
				w := &ReceivingWorker{window: NewReceivingWindow(), leftOver: buf.MultiBuffer{receivingTestBuffer(want)}}
				start := make(chan struct{})
				var wg sync.WaitGroup
				var partial []byte
				var whole buf.MultiBuffer
				wg.Go(func() {
					<-start
					for range 256 {
						var p [1]byte
						if w.Read(p[:]) != 0 {
							partial = append(partial, p[0])
						}
					}
				})
				wg.Go(func() { <-start; whole = w.ReadMultiBuffer() })
				if release {
					wg.Go(func() {
						<-start
						for range 256 {
							w.Release()
						}
					})
				}
				close(start)
				wg.Wait()
				w.Release()
				seen := [256]bool{}
				got := bytes.Clone(partial)
				for _, b := range whole {
					got = append(got, b.Bytes()...)
				}
				buf.ReleaseMulti(whole)
				for _, value := range got {
					if seen[value] {
						t.Fatalf("duplicated/corrupted transferred byte %d", value)
					}
					seen[value] = true
				}
				if !release && len(got) != len(want) {
					t.Fatalf("lost bytes: %d", len(got))
				}
				if !w.leftOver.IsEmpty() {
					t.Fatal("leftovers republished after release")
				}
			}
		})
	}
}
