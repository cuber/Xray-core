package buf_test

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type interruptTestReader struct {
	started chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (r *interruptTestReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	close(r.started)
	<-r.done
	return nil, io.EOF
}

func (r *interruptTestReader) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

type interruptOnlyTestReader struct{ reader *interruptTestReader }

func (r *interruptOnlyTestReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return r.reader.ReadMultiBuffer()
}

func (r *interruptOnlyTestReader) Interrupt() { r.reader.Close() }

func TestTimeoutWrapperInterrupt(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		for _, kind := range []string{"closable", "interruptible"} {
			name := kind + "/blocking"
			if timeout {
				name = kind + "/timed-out"
			}
			t.Run(name, func(t *testing.T) {
				raw := &interruptTestReader{started: make(chan struct{}), done: make(chan struct{})}
				var reader buf.Reader = raw
				if kind == "interruptible" {
					reader = &interruptOnlyTestReader{reader: raw}
				}
				wrapped := &buf.TimeoutWrapperReader{Reader: reader}
				result := make(chan error, 1)
				if timeout {
					mb, err := wrapped.ReadMultiBufferTimeout(time.Millisecond)
					if len(mb) != 0 || err != nil {
						t.Fatalf("unexpected timeout result: %v %v", mb, err)
					}
				}
				go func() {
					_, err := wrapped.ReadMultiBuffer()
					result <- err
				}()
				<-raw.started
				common.Interrupt(wrapped)
				select {
				case err := <-result:
					if err != io.EOF {
						t.Fatalf("read error = %v, want EOF", err)
					}
				case <-time.After(time.Second):
					// Always join the blocked read, including on the unfixed baseline.
					raw.Close()
					<-result
					t.Fatal("wrapper did not interrupt its underlying reader")
				}
			})
		}
	}
}
