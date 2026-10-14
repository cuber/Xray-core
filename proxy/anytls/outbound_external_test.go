package anytls_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	outboundfeature "github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
)

func TestAnyTLSExternalServers(t *testing.T) {
	testAnyTLSExternalServers(t, "")
}

func TestAnyTLSExternalServersChained(t *testing.T) {
	for _, protocol := range []string{"socks", "vless", "anytls", "hysteria"} {
		for _, entrance := range []string{"proxySettings", "dialerProxy"} {
			mode := entrance + "/" + protocol
			t.Run(mode, func(t *testing.T) { testAnyTLSExternalServers(t, mode) })
		}
	}
}

func testAnyTLSExternalServers(t *testing.T, mode string) {
	for _, kind := range []string{"SINGBOX", "MIHOMO"} {
		t.Run(kind, func(t *testing.T) {
			binary := os.Getenv("ANYTLS_" + kind)
			if binary == "" {
				if os.Getenv("ANYTLS_STRICT") == "1" {
					t.Fatal("strict acceptance requires ANYTLS_" + kind)
				}
				t.Skip("set ANYTLS_" + kind + " for independent server acceptance")
			}
			f, outbound := outboundFixture(t, mode)
			var observedHop *chainCountsAlias
			if mode != "" {
				manager := f.instance.GetFeature(outboundfeature.ManagerType()).(outboundfeature.Manager)
				observedHop = &chainCountsAlias{Handler: manager.GetHandler("hop"), tag: "observed-hop", done: make(chan struct{}, 4096)}
				if err := manager.AddHandler(context.Background(), observedHop); err != nil {
					t.Fatal(err)
				}
				value, err := outbound.SenderSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				sender := value.(*proxyman.SenderConfig)
				if strings.HasPrefix(mode, "proxySettings/") {
					sender.ProxySettings.Tag = observedHop.Tag()
				} else {
					sender.StreamSettings.SocketSettings.DialerProxy = observedHop.Tag()
				}
				outbound.SenderSettings = serial.ToTypedMessage(sender)
			}
			dir := t.TempDir()
			cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
			certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
			for path, data := range map[string]string{certPath: strings.Join(cert["certificate"].([]string), "\n"), keyPath: strings.Join(cert["key"].([]string), "\n")} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			port := unusedPort(t)
			var config map[string]any
			padding := []string{"stop=4", "0=64-64", "1=128-128", "2=256-256", "3=512-512"}
			if kind == "SINGBOX" {
				config = map[string]any{
					"log": map[string]any{"level": "error"},
					"inbounds": []any{map[string]any{"type": "anytls", "listen": "127.0.0.1", "listen_port": port,
						"padding_scheme": padding,
						"users":          []any{map[string]any{"name": "test", "password": "remote-secret"}},
						"tls":            map[string]any{"enabled": true, "certificate_path": certPath, "key_path": keyPath}}},
					"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"},
				}
			} else {
				config = map[string]any{
					"mode": "rule", "log-level": "error",
					"listeners": []any{map[string]any{"name": "anytls", "type": "anytls", "listen": "127.0.0.1", "port": port,
						"padding-scheme": strings.Join(padding, "\n"),
						"users":          map[string]string{"test": "remote-secret"}, "certificate": certPath, "private-key": keyPath}},
					"rules": []string{"MATCH,DIRECT"},
				}
			}
			stopServer := startExternalControl(t, binary, kind, filepath.Join(dir, "server"), config, port)
			decoded, _ := outbound.ProxySettings.GetInstance()
			if os.Getenv("ANYTLS_TEST_DEBUG") != "" {
				t.Logf("remote Core port=%d external port=%d", decoded.(*anytls.ClientConfig).Server.Port, port)
			}
			decoded.(*anytls.ClientConfig).Server.Port = uint32(port)
			outbound.ProxySettings = serial.ToTypedMessage(decoded)
			if _, err := f.api.RemoveOutbound(context.Background(), &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.api.AddOutbound(context.Background(), &handler.AddOutboundRequest{Outbound: outbound}); err != nil {
				t.Fatal(err)
			}
			if mode == "" {
				assertExternalSequentialReuse(t, f, outbound, port)
			}
			if err := f.alter("external", "external-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "external-secret")
			target := M.ParseSocksaddr(externalControlEcho(t, "tcp"))
			exchange := func(payload []byte) error {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				// Exercise the outbound without the unrelated fixture inbound's
				// 64-session/user quota limiting rapid concurrent stream churn.
				ctx = session.SetForcedOutboundTagToContext(ctx, "client")
				destination, err := xnet.ParseDestination("tcp:" + target.String())
				if err != nil {
					return err
				}
				conn, err := core.Dial(ctx, f.instance, destination)
				if err != nil {
					return err
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.Copy(conn, bytes.NewReader(payload)); err != nil {
					return err
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, got); err != nil {
					return err
				}
				if !bytes.Equal(got, payload) {
					return fmt.Errorf("mixed payload")
				}
				return nil
			}
			for _, size := range []int{1, 8192, 65535, 65536, 1024 * 1024} {
				conn, err := client.DialContext(context.Background(), target)
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				payload := make([]byte, size)
				rand.Read(payload)
				_, err = conn.Write(payload)
				got := make([]byte, size)
				if err == nil {
					_, err = io.ReadFull(conn, got)
				}
				conn.Close()
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("external TCP size=%d: %v", size, err)
				}
			}
			packets := openUoT(t, client)
			defer packets.Close()
			udp := M.ParseSocksaddr(externalControlEcho(t, "udp"))
			for _, size := range []int{1, 512, 8192} {
				payload := bytes.Repeat([]byte{byte(size)}, size)
				if err := packets.WritePacket(B.As(payload), udp); err != nil {
					t.Fatal(err)
				}
				got := B.NewSize(65535)
				from, err := packets.ReadPacket(got)
				valid := err == nil && from == udp && bytes.Equal(got.Bytes(), payload)
				got.Release()
				if !valid {
					t.Fatalf("external UDP size=%d source=%v: %v", size, from, err)
				}
			}
			packets.Close()
			var workers sync.WaitGroup
			start := make(chan struct{})
			failures := make(chan error, 32)
			for worker := 0; worker < 32; worker++ {
				workers.Add(1)
				go func(worker int) {
					defer workers.Done()
					<-start
					for round := 0; round < 16; round++ {
						payload := bytes.Repeat([]byte(fmt.Sprintf("worker=%02d round=%02d;", worker, round)), 1024)
						if err := exchange(payload); err != nil {
							failures <- fmt.Errorf("worker=%d round=%d: %w", worker, round, err)
							return
						}
					}
				}(worker)
			}
			close(start)
			workers.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			stopServer()
			if observedHop != nil {
				deadline := time.Now().Add(5 * time.Second)
				for observedHop.closed.Load() != observedHop.opened.Load() {
					if time.Now().After(deadline) {
						if os.Getenv("ANYTLS_TEST_DEBUG") != "" {
							stack := make([]byte, 1<<20)
							t.Logf("shutdown stacks:\n%s", stack[:runtime.Stack(stack, true)])
						}
						t.Fatalf("hop did not observe server shutdown: closed=%d opened=%d", observedHop.closed.Load(), observedHop.opened.Load())
					}
					time.Sleep(time.Millisecond)
				}
				if observedHop.opened.Load() == 0 {
					t.Fatal("business bypassed observed hop")
				}
			}
			if err := exchange([]byte("must-fail-while-server-down")); err == nil {
				t.Fatal("request succeeded with server stopped")
			}
			startExternalControl(t, binary, kind, filepath.Join(dir, "restarted"), config, port)
			// No fixture retry and no Core handler replacement: the existing pool
			// must discard failed sessions and dial the restarted peer itself.
			if err := exchange([]byte("fresh-request-after-restart")); err != nil {
				t.Fatal("recovery:", err)
			}
		})
	}
}
