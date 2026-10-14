package quic_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"testing"

	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/common/protocol/quic"
	"golang.org/x/crypto/hkdf"
)

type initialVersion struct {
	version     uint32
	first       byte
	salt, label string
}

var initialVersions = []initialVersion{
	{0xff00001d, 0xc0, "afbfec289993d24c9e9786f19c6111e04390a899", "quic"},
	{1, 0xc0, "38762cf7f55934b34d179ae6a4c80cadccbb7f0a", "quic"},
	{0x6b3343cf, 0xd0, "0dede3def700a6db819381be6e269dcbf9bd2ed9", "quicv2"},
}

func initialHeader(v initialVersion, pn, payloadLen int) []byte {
	h := []byte{v.first | byte(pn-1)}
	h = binary.BigEndian.AppendUint32(h, v.version)
	h = append(h, 8, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0) // DCID, empty SCID and token
	return quicvarint.Append(h, uint64(payloadLen))
}

func TestQUICTruncatedProtectionSamples(t *testing.T) {
	for _, v := range initialVersions {
		for pn := 1; pn <= 4; pn++ {
			for sample := 0; sample < aes.BlockSize; sample++ {
				t.Run(fmt.Sprintf("version=%x/pn=%d/sample=%d", v.version, pn, sample), func(t *testing.T) {
					packet := append(initialHeader(v, pn, 4+sample), make([]byte, 4+sample)...)
					if h, err := quic.SniffQUIC(packet); err == nil || h != nil {
						t.Fatalf("truncated sample accepted: %v %v", h, err)
					}
				})
			}
		}
	}
}

func initialLabel(t testing.TB, secret []byte, label string, n int) []byte {
	t.Helper()
	info := binary.BigEndian.AppendUint16(nil, uint16(n))
	info = append(info, byte(len("tls13 ")+len(label)))
	info = append(info, "tls13 "+label...)
	info = append(info, 0)
	out := make([]byte, n)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, secret, info), out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Build the wire fixture independently with standard TLS and AES-GCM, not
// the sniffer's key-expansion or AEAD helpers.
func protectedInitial(t testing.TB, v initialVersion, pn int, frames []byte) []byte {
	t.Helper()
	salt, err := hex.DecodeString(v.salt)
	if err != nil {
		t.Fatal(err)
	}
	secret := initialLabel(t, hkdf.Extract(sha256.New, []byte{1, 2, 3, 4, 5, 6, 7, 8}, salt), "client in", 32)
	block, err := aes.NewCipher(initialLabel(t, secret, v.label+" key", 16))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	frames = append(bytes.Clone(frames), make([]byte, 32)...)
	h := initialHeader(v, pn, pn+len(frames)+aead.Overhead())
	offset := len(h)
	h = append(h, make([]byte, pn)...)
	h[len(h)-1] = 1
	nonce := initialLabel(t, secret, v.label+" iv", 12)
	nonce[len(nonce)-1] ^= 1
	packet := aead.Seal(bytes.Clone(h), nonce, frames, h)
	hp, err := aes.NewCipher(initialLabel(t, secret, v.label+" hp", 16))
	if err != nil {
		t.Fatal(err)
	}
	mask := make([]byte, 16)
	hp.Encrypt(mask, packet[offset+4:offset+20])
	packet[0] ^= mask[0] & 15
	for i := 0; i < pn; i++ {
		packet[offset+i] ^= mask[i+1]
	}
	return packet
}

func clientHelloFrame(t testing.TB) []byte {
	t.Helper()
	c := tls.QUICClient(&tls.QUICConfig{TLSConfig: &tls.Config{ServerName: "owned.example.test", MinVersion: tls.VersionTLS13}})
	defer c.Close()
	c.SetTransportParameters(nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		e := c.NextEvent()
		if e.Kind == tls.QUICNoEvent {
			t.Fatal("TLS did not emit a ClientHello")
		}
		if e.Kind == tls.QUICWriteData && e.Level == tls.QUICEncryptionLevelInitial {
			frame := quicvarint.Append([]byte{6, 0}, uint64(len(e.Data)))
			return append(frame, e.Data...)
		}
	}
}

func TestQUICGeneratedInitialPreservesSNI(t *testing.T) {
	frame := clientHelloFrame(t)
	for _, v := range initialVersions {
		for pn := 1; pn <= 4; pn++ {
			t.Run(fmt.Sprintf("version=%x/pn=%d", v.version, pn), func(t *testing.T) {
				h, err := quic.SniffQUIC(protectedInitial(t, v, pn, frame))
				if err != nil || h == nil || h.Domain() != "owned.example.test" || h.Protocol() != "quic" {
					t.Fatalf("valid Initial: header=%v error=%v", h, err)
				}
			})
		}
	}
}

func TestQUICLengthBoundaries(t *testing.T) {
	for length := 0; length <= 4; length++ {
		packet := append(initialHeader(initialVersions[1], 1, length), make([]byte, length)...)
		if h, err := quic.SniffQUIC(packet); err == nil || h != nil {
			t.Fatalf("packet length %d accepted", length)
		}
	}
	if h, err := quic.SniffQUIC(nil); err == nil || h != nil {
		t.Fatal("empty packet accepted")
	}
	for _, offset := range []uint64{32767, 65535, 65536, (1 << 62) - 1} {
		frame := quicvarint.Append([]byte{6}, offset)
		frame = append(frame, 1, 1)
		if h, err := quic.SniffQUIC(protectedInitial(t, initialVersions[1], 4, frame)); err == nil || h != nil {
			t.Fatalf("oversized CRYPTO offset %d accepted", offset)
		}
	}
}

func FuzzSniffQUICBounded(f *testing.F) {
	f.Add([]byte{})
	frame := clientHelloFrame(f)
	for _, v := range initialVersions {
		for pn := 1; pn <= 4; pn++ {
			f.Add(protectedInitial(f, v, pn, frame))
			for sample := 0; sample < 16; sample++ {
				f.Add(append(initialHeader(v, pn, 4+sample), make([]byte, 4+sample)...))
			}
		}
	}
	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > 65535 {
			return
		}
		h, err := quic.SniffQUIC(bytes.Clone(packet))
		if err == nil && (h == nil || h.Protocol() != "quic") {
			t.Fatalf("invalid successful decode: %v", h)
		}
	})
}
