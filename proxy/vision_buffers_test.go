package proxy

import (
	"bytes"
	"context"
	stdtls "crypto/tls"
	"runtime"
	"sync"
	"testing"

	utls "github.com/refraction-networking/utls"
	realitylib "github.com/xtls/reality"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/proxy/vless/encryption"
)

type inlineVisionConn struct {
	before   uint64
	input    bytes.Reader
	rawInput bytes.Buffer
	after    uint64
}

type pooledVisionConn struct {
	before   uint64
	in       struct{ sync.Mutex }
	input    bytes.Reader
	rawInput *bytes.Buffer
	after    uint64
}

func takeVisionBytes(t *testing.T, b *VisionBuffers) []byte {
	t.Helper()
	mb, err := b.Take()
	if err != nil {
		t.Fatal(err)
	}
	defer buf.ReleaseMulti(mb)
	var result []byte
	for _, part := range mb {
		result = append(result, part.Bytes()...)
	}
	return result
}

func TestVisionBuffersDependencyLayouts(t *testing.T) {
	for name, owner := range map[string]any{
		"tls": new(stdtls.Conn), "utls": new(utls.Conn),
		"reality": new(realitylib.Conn), "encryption": new(encryption.CommonConn),
	} {
		t.Run(name, func(t *testing.T) {
			b, err := NewVisionBuffers(owner)
			if err != nil {
				t.Fatal(err)
			}
			if b.indirect != (name == "reality") {
				t.Fatal("unexpected rawInput representation")
			}
			if (b.readMu != nil) != (name != "encryption") {
				t.Fatal("unexpected read lock")
			}
			if got := takeVisionBytes(t, b); len(got) != 0 {
				t.Fatalf("unexpected pending data: %x", got)
			}
		})
	}
}

func TestVisionBuffersInlineTransfer(t *testing.T) {
	c := &inlineVisionConn{before: 123, after: 456}
	c.rawInput.WriteString("plaintextwire")
	c.input.Reset(c.rawInput.Next(len("plaintext")))
	b, err := NewVisionBuffers(c)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if got := string(takeVisionBytes(t, b)); got != "plaintextwire" {
		t.Fatal(got)
	}
	if c.input.Len() != 0 || c.rawInput.Cap() != 0 || c.before != 123 || c.after != 456 {
		t.Fatal("incorrect field release")
	}
	if _, err := b.Take(); err == nil {
		t.Fatal("second transfer accepted")
	}
}

func TestVisionBuffersLazyPoolTransfer(t *testing.T) {
	for _, initialNil := range []bool{false, true} {
		t.Run(map[bool]string{false: "replaced", true: "initially-nil"}[initialNil], func(t *testing.T) {
			old := bytes.NewBufferString("returned-to-pool")
			c := &pooledVisionConn{before: 123, after: 456}
			if !initialNil {
				c.rawInput = old
			}
			b, err := NewVisionBuffers(c)
			if err != nil {
				t.Fatal(err)
			}
			c.rawInput = bytes.NewBufferString("plaintextwire")
			c.input.Reset(c.rawInput.Next(len("plaintext")))
			current := c.rawInput
			// A different connection now owns the previous pool buffer.
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 1000 {
					old.Reset()
					old.WriteString("other-connection")
				}
			}()
			runtime.GC()
			mb, err := b.Take()
			if err != nil {
				t.Fatal(err)
			}
			defer buf.ReleaseMulti(mb)
			current.Reset()
			current.WriteString("reused-storage")
			var got []byte
			for _, part := range mb {
				got = append(got, part.Bytes()...)
			}
			<-done
			if string(got) != "plaintextwire" {
				t.Fatalf("transfer aliases or lost data: %q", got)
			}
			if c.rawInput != nil || c.input.Len() != 0 || c.before != 123 || c.after != 456 || old.String() != "other-connection" {
				t.Fatal("incorrect pool ownership transfer")
			}
		})
	}
}

func TestVisionBuffersRejectUnsupported(t *testing.T) {
	var nilConn *inlineVisionConn
	for _, owner := range []any{nil, nilConn, inlineVisionConn{}, &struct{}{},
		&struct {
			input    bytes.Reader
			rawInput []byte
		}{},
		&struct {
			input    bytes.Buffer
			rawInput bytes.Buffer
		}{},
		&struct{ inlineVisionConn }{},
		&struct {
			input    bytes.Reader
			rawInput bytes.Buffer
			in       int
		}{},
	} {
		if _, err := NewVisionBuffers(owner); err == nil {
			t.Fatalf("accepted unsupported %T", owner)
		}
	}
	if _, err := (*VisionBuffers)(nil).Take(); err == nil {
		t.Fatal("nil transfer accepted")
	}
}

func TestVisionBuffersPoolReturnedBeforeSwitch(t *testing.T) {
	old := bytes.NewBufferString("previous-record")
	c := &pooledVisionConn{rawInput: old}
	b, err := NewVisionBuffers(c)
	if err != nil {
		t.Fatal(err)
	}
	c.rawInput = nil
	old.Reset()
	old.WriteString("another-owner")
	if got := takeVisionBytes(t, b); len(got) != 0 || old.String() != "another-owner" {
		t.Fatal("touched returned pool buffer")
	}
}

func TestVisionBuffersOwnerSurvivesGC(t *testing.T) {
	want := bytes.Repeat([]byte("pending-data"), 4096)
	makeBuffers := func() *VisionBuffers {
		c := &pooledVisionConn{rawInput: bytes.NewBuffer(bytes.Clone(want))}
		c.input.Reset(c.rawInput.Next(12345))
		b, err := NewVisionBuffers(c)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := makeBuffers()
	for range 3 {
		runtime.GC()
	}
	if got := takeVisionBytes(t, b); !bytes.Equal(got, want) {
		t.Fatal("lost connection or multi-buffer contents across GC")
	}
}

type visionTestReader struct{ data buf.MultiBuffer }

type visionFreshReader struct{}

func (visionFreshReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	b := buf.New()
	b.WriteString("plaintext")
	return buf.MultiBuffer{b}, nil
}

type visionDiscardWriter struct{}

func (visionDiscardWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	buf.ReleaseMulti(mb)
	return nil
}

func TestVisionConcurrentDetection(t *testing.T) {
	state := NewTrafficState(nil)
	state.NumberOfPacketToFilter = 200
	r := NewVisionReader(visionFreshReader{}, state, true, context.Background(), nil, nil, nil)
	w := NewVisionWriter(visionDiscardWriter{}, state, false, context.Background(), nil, nil, nil)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			mb, err := r.ReadMultiBuffer()
			if err != nil {
				t.Error(err)
			}
			buf.ReleaseMulti(mb)
		}
	})
	wg.Go(func() {
		for range 100 {
			mb, _ := (visionFreshReader{}).ReadMultiBuffer()
			if err := w.WriteMultiBuffer(mb); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	if state.NumberOfPacketToFilter != 0 {
		t.Fatalf("lost detection updates: %d", state.NumberOfPacketToFilter)
	}
}

func (r *visionTestReader) ReadMultiBuffer() (buf.MultiBuffer, error) { return r.data, nil }

func TestVisionReaderMissingBuffersFailsClosed(t *testing.T) {
	state := NewTrafficState(nil)
	state.NumberOfPacketToFilter = 0
	state.Inbound.CurrentCommand = 2
	state.Inbound.RemainingContent = 0
	state.Inbound.RemainingPadding = 0
	part := buf.New()
	part.WriteString("payload")
	r := &visionTestReader{data: buf.MultiBuffer{part}}
	v := NewVisionReader(r, state, true, context.Background(), nil, nil, nil)
	mb, err := v.ReadMultiBuffer()
	if err == nil || len(mb) != 0 || state.Inbound.UplinkReaderDirectCopy || v.Reader != r {
		t.Fatal("failed transfer enabled raw reads")
	}
}
