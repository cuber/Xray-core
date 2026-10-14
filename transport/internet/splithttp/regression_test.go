package splithttp

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/xtls/xray-core/common/buf"
	"google.golang.org/protobuf/proto"
)

type releasingWriter struct{ got []byte }

func (w *releasingWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for _, b := range mb {
		w.got = append(w.got, b.Bytes()...)
	}
	buf.ReleaseMulti(mb)
	return nil
}
func TestUploadWriterReleasedLength(t *testing.T) {
	downstream := new(releasingWriter)
	w := uploadWriter{Writer: downstream}
	payload := bytes.Repeat([]byte{0x71}, 4096)
	n, err := w.Write(payload)
	if n != len(payload) || err != nil || !bytes.Equal(payload, downstream.got) {
		t.Fatalf("Write = %d, %v; delivered %d", n, err, len(downstream.got))
	}
}

type regressionConn struct{}

func (*regressionConn) IsClosed() bool { return false }
func TestNormalizationSharedConfig(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		c := &Config{Xmux: &XmuxConfig{}}
		if explicit {
			c.ScMaxEachPostBytes = &RangeConfig{From: 4096, To: 8192}
			c.UplinkChunkSize = &RangeConfig{From: 1, To: 32}
			c.Xmux.MaxConnections = &RangeConfig{From: 2, To: 2}
			c.Xmux.MaxConcurrency = &RangeConfig{From: 3, To: 3}
		}
		before := proto.Clone(c)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				m := NewXmuxManager(c.Xmux, func() XmuxConn { return &regressionConn{} })
				for range 1000 {
					post, chunk := c.GetNormalizedScMaxEachPostBytes(), c.GetNormalizedUplinkChunkSize()
					if explicit {
						if post.From != 4096 || post.To != 8192 || chunk.From != 64 || chunk.To != 64 || m.connections != 2 || m.concurrency != 3 {
							t.Error("explicit normalization changed")
						}
					} else if post.From != 1000000 || post.To != 1000000 || chunk.From != 1000000 || m.connections != 0 || m.concurrency != 0 {
						t.Error("default normalization changed")
					}
					m.GetXmuxClient(context.Background())
					c.GetNormalizedScMinPostsIntervalMs()
					c.GetNormalizedScStreamUpServerSecs()
				}
			})
		}
		wg.Wait()
		if !proto.Equal(c, before) {
			t.Fatalf("normalization mutated shared proto: %v", c)
		}
	}
}
