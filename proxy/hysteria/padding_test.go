package hysteria

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// Core spec025 FR-001: the TCP headers, padded as before or with padding off.

const hy2PaddingChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// For a fixed padding string the encoders write what they wrote before spec025, byte for byte.
func TestTCPHeaderEncodingsUnchanged(t *testing.T) {
	long := strings.Repeat("p", 64) // a two-byte length varint
	for _, c := range []struct {
		name  string
		write func(*bytes.Buffer) error
		want  []byte
	}{
		{
			"request", func(b *bytes.Buffer) error { return writeTCPRequest(b, "example.com:443", "xyz") },
			append(append([]byte{15}, "example.com:443"...), append([]byte{3}, "xyz"...)...),
		},
		{
			"request, long padding", func(b *bytes.Buffer) error { return writeTCPRequest(b, "a:1", long) },
			append(append([]byte{3}, "a:1"...), append([]byte{0x40, 64}, long...)...),
		},
		{
			"response", func(b *bytes.Buffer) error { return writeTCPResponse(b, true, "", "abc") },
			append([]byte{0, 0, 3}, "abc"...),
		},
		{
			"error response", func(b *bytes.Buffer) error { return writeTCPResponse(b, false, "no", long) },
			append(append([]byte{1, 2}, "no"...), append([]byte{0x40, 64}, long...)...),
		},
	} {
		var b bytes.Buffer
		if err := c.write(&b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b.Bytes(), c.want) {
			t.Errorf("%s: %x, want %x", c.name, b.Bytes(), c.want)
		}
	}
}

// requestPadding and responsePadding read back the padding a header carries.
func requestPadding(t *testing.T, b []byte) string {
	t.Helper()
	r := bytes.NewReader(b)
	n, err := quicvarint.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Seek(int64(n), io.SeekCurrent)
	return trailingPadding(t, r)
}

func responsePadding(t *testing.T, b []byte) string {
	t.Helper()
	r := bytes.NewReader(b[1:])
	n, err := quicvarint.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Seek(int64(n), io.SeekCurrent)
	return trailingPadding(t, r)
}

func trailingPadding(t *testing.T, r *bytes.Reader) string {
	t.Helper()
	n, err := quicvarint.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	padding := make([]byte, n)
	if _, err := io.ReadFull(r, padding); err != nil || r.Len() != 0 {
		t.Fatalf("padding length %d, %d bytes left, %v", n, r.Len(), err)
	}
	return string(padding)
}

// With padding, the lengths stay in today's half-open ranges and come within 5 of both ends over 10 000
// headers per message.
func TestTCPHeaderPaddingRanges(t *testing.T) {
	for _, c := range []struct {
		name     string
		min, max int // half-open
		write    func(*bytes.Buffer) error
		padding  func(*testing.T, []byte) string
	}{
		{"request", 64, 512, func(b *bytes.Buffer) error { return WriteTCPRequest(b, "example.com:443", true) }, requestPadding},
		{"response", 128, 1024, func(b *bytes.Buffer) error { return WriteTCPResponse(b, true, "", true) }, responsePadding},
	} {
		low, high := c.max, 0
		for range 10000 {
			var b bytes.Buffer
			if err := c.write(&b); err != nil {
				t.Fatal(err)
			}
			padding := c.padding(t, b.Bytes())
			if len(padding) < c.min || len(padding) >= c.max || strings.Trim(padding, hy2PaddingChars) != "" {
				t.Fatalf("%s: a padding of %d characters", c.name, len(padding))
			}
			low, high = min(low, len(padding)), max(high, len(padding))
		}
		if low > c.min+5 || high < c.max-1-5 {
			t.Errorf("%s: lengths %d to %d over 10000 headers, range [%d, %d)", c.name, low, high, c.min, c.max)
		}
	}
}

// With padding off, the length is 0 and nothing follows it; the readers take both forms and leave the payload.
func TestTCPHeadersWithoutPadding(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTCPRequest(&b, "example.com:443", false); err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte{15}, "example.com:443"...), 0); !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("request %x, want %x", b.Bytes(), want)
	}
	if err := WriteTCPResponse(&b, true, "", false); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0, 0, 0}; !bytes.Equal(b.Bytes()[17:], want) {
		t.Fatalf("response %x, want %x", b.Bytes()[17:], want)
	}

	for _, padding := range []bool{false, true} {
		var wire bytes.Buffer
		WriteTCPRequest(&wire, "example.com:443", padding)
		wire.WriteString("payload")
		WriteTCPResponse(&wire, true, "", padding)
		wire.WriteString("answer")
		if addr, err := ReadTCPRequest(&wire); err != nil || addr != "example.com:443" {
			t.Fatalf("padding %v: request %q, %v", padding, addr, err)
		}
		if payload := wire.Next(7); string(payload) != "payload" {
			t.Fatalf("padding %v: after the request %q", padding, payload)
		}
		if ok, msg, err := ReadTCPResponse(&wire); err != nil || !ok || msg != "" {
			t.Fatalf("padding %v: response %v %q %v", padding, ok, msg, err)
		}
		if answer := wire.String(); answer != "answer" {
			t.Fatalf("padding %v: after the response %q", padding, answer)
		}
	}
}

type paddingConn struct {
	net.Conn
	padding bool
}

func (c paddingConn) HysteriaPadding() bool { return c.padding }

// The proxy pads as the stream says, through the stats wrapper, and keeps padding on a connection that does not
// say.
func TestSendsPaddingFollowsTheStream(t *testing.T) {
	plain, other := net.Pipe()
	defer plain.Close()
	defer other.Close()
	for _, c := range []struct {
		conn net.Conn
		want bool
	}{
		{paddingConn{Conn: plain, padding: false}, false},
		{paddingConn{Conn: plain, padding: true}, true},
		{&stat.CounterConnection{Connection: paddingConn{Conn: plain, padding: false}}, false},
		{plain, true},
	} {
		if got := sendsPadding(c.conn); got != c.want {
			t.Errorf("%T: padding %v, want %v", c.conn, got, c.want)
		}
	}
}
