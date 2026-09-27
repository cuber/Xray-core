package scenarios

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	stdtls "crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	stdnet "net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	realitylib "github.com/xtls/reality"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/protocol/tls/cert"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/vless"
	vin "github.com/xtls/xray-core/proxy/vless/inbound"
	vout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/testing/servers/dnsfixture"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tls"
)

// These cores run in the test process: -race/checkptr and runtime.GC cover
// the input/rawInput pointers, unlike the subprocess scenario helpers.
func TestVlessVisionGCMatrix(t *testing.T) {
	for _, mode := range []string{"tls", "tls-native", "tls-bad-pin", "reality", "encrypted-raw-0", "encrypted-raw-1", "encrypted-raw-2", "encrypted-tls"} {
		t.Run(mode, func(t *testing.T) {
			innerCert, roots := dnsfixture.Certificate(t)
			target, err := stdtls.Listen("tcp", "127.0.0.1:0", &stdtls.Config{Certificates: []stdtls.Certificate{innerCert}, MinVersion: stdtls.VersionTLS13})
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			workers.Go(func() {
				for {
					c, err := target.Accept()
					if err != nil {
						return
					}
					workers.Go(func() { defer c.Close(); c.SetDeadline(time.Now().Add(15 * time.Second)); io.Copy(c, c) })
				}
			})
			defer func() { target.Close(); workers.Wait() }()
			targetPort := net.Port(target.Addr().(*stdnet.TCPAddr).Port)
			serverPort, clientPort := tcp.PickPort(), tcp.PickPort()
			userID := uuid.New()
			id := userID.String()
			serverStream := &internet.StreamConfig{ProtocolName: "tcp"}
			clientStream := &internet.StreamConfig{ProtocolName: "tcp"}
			var realityTarget string
			account := &vless.Account{Id: id, Flow: vless.XRV}
			inbound := &vin.Config{Clients: []*protocol.User{{Account: serial.ToTypedMessage(&vless.Account{Id: id, Flow: vless.XRV})}}}
			if strings.HasPrefix(mode, "tls") || mode == "encrypted-tls" {
				certificate, hash := cert.MustGenerate(nil, cert.CommonName("localhost"))
				staticCertificate := tls.ParseCertificate(certificate)
				serverStream.SecurityType = serial.GetMessageType(&tls.Config{})
				serverStream.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(&tls.Config{Certificate: []*tls.Certificate{staticCertificate}})}
				clientStream.SecurityType = serial.GetMessageType(&tls.Config{})
				clientTLS := &tls.Config{PinnedPeerCertSha256: [][]byte{hash[:]}}
				if mode == "tls-native" {
					clientTLS.Fingerprint = "unsafe"
				}
				if mode == "tls-bad-pin" {
					clientTLS.PinnedPeerCertSha256 = [][]byte{make([]byte, 32)}
				}
				clientStream.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(clientTLS)}
			}
			if mode == "reality" {
				key, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				shortID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
				serverStream.SecurityType = serial.GetMessageType(&reality.Config{})
				realityTarget = localRealityTarget(t)
				serverStream.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(&reality.Config{Dest: realityTarget, Type: "tcp", ServerNames: []string{"reality.test"}, PrivateKey: key.Bytes(), ShortIds: [][]byte{shortID}})}
				clientStream.SecurityType = serial.GetMessageType(&reality.Config{})
				clientStream.SecuritySettings = []*serial.TypedMessage{serial.ToTypedMessage(&reality.Config{ServerName: "reality.test", Fingerprint: "chrome", PublicKey: key.PublicKey().Bytes(), ShortId: shortID})}
			}
			if strings.HasPrefix(mode, "encrypted-") {
				key, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				inbound.Decryption = base64.RawURLEncoding.EncodeToString(key.Bytes())
				account.Encryption = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
				if mode == "encrypted-raw-1" {
					inbound.XorMode = 1
					account.XorMode = 1
				}
				if mode == "encrypted-raw-2" {
					inbound.XorMode = 2
					account.XorMode = 2
				}
			}
			serverConfig := &core.Config{
				Inbound: []*core.InboundHandlerConfig{{
					ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: net.NewIPOrDomain(net.LocalHostIP), PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}}, StreamSettings: serverStream}),
					ProxySettings:    serial.ToTypedMessage(inbound),
				}},
				Outbound: []*core.OutboundHandlerConfig{{ProxySettings: serial.ToTypedMessage(&freedom.Config{IpsBlocked: &freedom.IPRules{}})}},
			}
			clientConfig := &core.Config{
				Inbound: []*core.InboundHandlerConfig{{
					ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: net.NewIPOrDomain(net.LocalHostIP), PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}}}),
					ProxySettings:    serial.ToTypedMessage(&dokodemo.Config{Address: net.NewIPOrDomain(net.LocalHostIP), Port: uint32(targetPort), Networks: []net.Network{net.Network_TCP}}),
				}},
				Outbound: []*core.OutboundHandlerConfig{{
					SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{StreamSettings: clientStream}),
					ProxySettings:  serial.ToTypedMessage(&vout.Config{Vnext: &protocol.ServerEndpoint{Address: net.NewIPOrDomain(net.LocalHostIP), Port: uint32(serverPort), User: &protocol.User{Account: serial.ToTypedMessage(account)}}}),
				}},
			}
			for _, config := range []*core.Config{serverConfig, clientConfig} {
				instance, err := core.New(withDefaultApps(config))
				if err != nil {
					t.Fatal(err)
				}
				defer instance.Close()
				if err := instance.Start(); err != nil {
					t.Fatal(err)
				}
			}
			if realityTarget != "" {
				// Wait for the real asynchronous target probes, rather than racing
				// their first five-second handshake retry.
				deadline := time.Now().Add(10 * time.Second)
				for {
					value, _ := realitylib.GlobalPostHandshakeRecordsLens.Load(realityTarget + " reality.test 2")
					if _, ready := value.([]int); ready {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("REALITY target probe did not complete")
					}
					time.Sleep(time.Millisecond)
				}
			}
			stop, done := make(chan struct{}), make(chan struct{})
			var cycles atomic.Int32
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
						runtime.GC()
						cycles.Add(1)
					}
				}
			}()
			defer func() { close(stop); <-done }()
			payload := bytes.Repeat([]byte("vision-gc-0123456"), 4096)
			for attempt := 0; attempt < 3; attempt++ {
				c, err := stdtls.DialWithDialer(&stdnet.Dialer{Timeout: 5 * time.Second}, "tcp", fmt.Sprintf("127.0.0.1:%d", clientPort), &stdtls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: stdtls.VersionTLS13})
				if mode == "tls-bad-pin" {
					if err == nil {
						c.Close()
						t.Fatal("wrong outer certificate pin accepted")
					}
					t.Logf("wrong outer certificate pin rejected: %v", err)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				c.SetDeadline(time.Now().Add(10 * time.Second))
				if _, err := c.Write(payload); err != nil {
					c.Close()
					t.Fatal(err)
				}
				got := make([]byte, len(payload))
				_, err = io.ReadFull(c, got)
				if err != nil || !bytes.Equal(got, payload) {
					c.Close()
					t.Fatalf("attempt %d echo: %v", attempt, err)
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if cycles.Load() == 0 {
				t.Fatal("GC did not run during transfer")
			}
			t.Logf("%s: three TLS 1.3 inner connections, 65536 bytes each, GC cycles=%d", mode, cycles.Load())
		})
	}
}
