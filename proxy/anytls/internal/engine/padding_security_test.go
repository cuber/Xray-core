package anytls

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestPaddingValidationBounds(t *testing.T) {
	for name, scheme := range map[string]string{
		"empty": "", "missing stop": "1=1-2", "negative stop": "stop=-1",
		"zero stop": "stop=0", "large stop": "stop=257", "duplicate": "stop=8\nstop=9",
		"duplicate record": "stop=8\n1=1-2\n1=2-3", "index": "stop=8\n256=1-2",
		"index alias": "stop=8\n01=1-2", "unknown": "stop=8\nx=1-2",
		"malformed": "stop=8\n1=no", "negative": "stop=8\n1=-1-2", "zero": "stop=8\n1=0-1",
		"reversed": "stop=8\n1=2-1", "too large": "stop=8\n1=65536-65536",
		"max int":          "stop=8\n1=" + strconv.Itoa(int(^uint(0)>>1)) + "-" + strconv.Itoa(int(^uint(0)>>1)),
		"integer overflow": "stop=8\n1=18446744073709551616-18446744073709551616",
		"ranges":           "stop=8\n1=" + strings.Repeat("c,", 16) + "c",
		"packet budget":    "stop=8\n1=65535-65535,65535-65535,65535-65535",
		"text budget":      strings.Repeat("x", maxPaddingSchemeBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newPaddingFactory([]byte(scheme)); err == nil {
				t.Fatal("unsafe scheme accepted")
			}
		})
	}
	budget := "stop=256"
	for i := 0; i < 8; i++ {
		budget += fmt.Sprintf("\n%d=65535-65535,65535-65535", i)
	}
	for _, good := range []string{string(DefaultPaddingScheme), "stop=1", "stop=256\n0=65535-65535\n255=1-1,c", budget} {
		if err := ValidatePaddingScheme([]byte(good)); err != nil {
			t.Fatalf("valid boundary rejected: %v", err)
		}
	}
	if err := ValidatePaddingScheme([]byte(budget + "\n8=65535-65535")); err == nil {
		t.Fatal("total budget not enforced")
	}
}

func TestPaddingFactoryOwnsScheme(t *testing.T) {
	raw := append([]byte(nil), DefaultPaddingScheme...)
	f, err := newPaddingFactory(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'X'
	if !bytes.Equal(f.rawScheme, DefaultPaddingScheme) {
		t.Fatal("scheme aliases caller buffer")
	}
}

func TestRemotePaddingFramesRejectAndRecover(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	old := c.padding.Load()
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	defer s.Close()
	for _, raw := range []string{"stop=8\n1=9223372036854775807-9223372036854775807", "stop=8\n0=65536-65536", "stop=-1", "stop=8\n1=1-2\n1=2-3"} {
		frame := make([]byte, frameOverhead+len(raw))
		putFrameHeader(frame, commandUpdatePaddingScheme, 0, len(raw))
		copy(frame[frameOverhead:], raw)
		s.reader = bufio.NewReader(bytes.NewReader(frame))
		if err := s.readLoop(); err != io.EOF {
			t.Fatal(err)
		}
		if c.padding.Load() != old {
			t.Fatal("invalid remote frame replaced shared factory")
		}
		if err := s.writePacketLocked([]byte("still safe")); err != nil {
			t.Fatal(err)
		}
	}
	good := "stop=8\n0=64-64\n1=128-128"
	frame := make([]byte, frameOverhead+len(good))
	putFrameHeader(frame, commandUpdatePaddingScheme, 0, len(good))
	copy(frame[frameOverhead:], good)
	s.reader = bufio.NewReader(bytes.NewReader(frame))
	if err := s.readLoop(); err != io.EOF {
		t.Fatal(err)
	}
	if c.padding.Load() == old || c.padding.Load().GenerateHandshakePaddingSize() != 64 {
		t.Fatal("valid update did not recover")
	}
	other, _ := NewClient(ClientOptions{Password: "other"})
	defer other.Close()
	if !bytes.Equal(other.padding.Load().rawScheme, DefaultPaddingScheme) {
		t.Fatal("update crossed pool boundary")
	}
}

func TestPaddingAllocationDefense(t *testing.T) {
	for _, size := range []int{-1, 0, 65536, int(^uint(0) >> 1)} {
		if _, err := paddingRecordSize(size); err == nil {
			t.Fatal("unsafe size accepted", size)
		}
	}
	if n, err := paddingRecordSize(65535); err != nil || n != 65535+frameOverhead {
		t.Fatal(n, err)
	}
	c, _ := NewClient(ClientOptions{Password: "test"})
	defer c.Close()
	c.padding.Store(&paddingFactory{stop: 8, records: map[uint32][]paddingRange{1: {{minSize: int(^uint(0) >> 1), maxSize: int(^uint(0) >> 1)}}}})
	s := newClientSession(c, &finiteConn{Reader: bytes.NewReader(nil)})
	defer s.Close()
	if err := s.writePacketLocked([]byte("payload")); err == nil {
		t.Fatal("allocation boundary accepted overflow")
	}
}

func TestPaddingHandshakeAllocationDefense(t *testing.T) {
	c, err := NewClient(ClientOptions{Password: "test", DialOut: func(context.Context) (net.Conn, error) {
		return &finiteConn{Reader: bytes.NewReader(nil)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, size := range []int{65536, int(^uint(0) >> 1)} {
		c.padding.Store(&paddingFactory{stop: 8, records: map[uint32][]paddingRange{0: {{minSize: size, maxSize: size}}}})
		if _, err := c.createSession(context.Background()); err == nil {
			t.Fatal("oversized handshake accepted")
		}
		if c.creating != 0 || len(c.sessions) != 0 {
			t.Fatal("rejected handshake leaked capacity")
		}
	}
}

func FuzzRemotePaddingScheme(f *testing.F) {
	f.Add(DefaultPaddingScheme)
	f.Add([]byte("stop=8\n0=65535-65535\n1=9223372036854775807-9223372036854775807"))
	f.Add([]byte("stop=-1"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		factory, err := newPaddingFactory(raw)
		if err != nil {
			return
		}
		if len(raw) > maxPaddingSchemeBytes || factory.stop == 0 || factory.stop > maxPaddingRecords {
			t.Fatal("unbounded factory")
		}
		for packet := range factory.records {
			budget := 0
			for _, size := range factory.GenerateRecordPayloadSizes(packet) {
				if size == paddingCheckMark {
					continue
				}
				if _, err := paddingRecordSize(size); err != nil {
					t.Fatal(err)
				}
				budget += size
			}
			if budget > maxPaddingPacketBytes {
				t.Fatal("unbounded packet")
			}
		}
	})
}
