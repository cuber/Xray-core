package anytls_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
)

func writeFrame(c net.Conn, command byte, id uint32, payload []byte) error {
	frame := make([]byte, 7+len(payload))
	frame[0] = command
	binary.BigEndian.PutUint32(frame[1:5], id)
	binary.BigEndian.PutUint16(frame[5:7], uint16(len(payload)))
	copy(frame[7:], payload)
	_, err := c.Write(frame)
	return err
}

func rawSession(t *testing.T, f *integrationFixture, password string) net.Conn {
	t.Helper()
	c, err := tls.Dial("tcp", f.address, f.tls)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	hash := sha256.Sum256([]byte(password))
	// Deliberate single-byte writes verify authentication fragmentation.
	for _, b := range append(hash[:], 0, 0) {
		if _, err := c.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFrame(c, 4, 0, []byte("v=2\n")); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnyTLSSingleSession100StreamsAndRevocation(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.alter("mux-user", "mux-secret", false); err != nil {
		t.Fatal(err)
	}
	c := rawSession(t, f, "mux-secret")
	done := make(chan error, 1)
	go func() {
		responses := make(map[uint32][]byte)
		completed := make(map[uint32]bool)
		acknowledged := make(map[uint32]bool)
		for len(completed) < 100 || len(acknowledged) < 100 {
			var header [7]byte
			if _, err := io.ReadFull(c, header[:]); err != nil {
				done <- err
				return
			}
			data := make([]byte, binary.BigEndian.Uint16(header[5:]))
			if _, err := io.ReadFull(c, data); err != nil {
				done <- err
				return
			}
			id := binary.BigEndian.Uint32(header[1:5])
			if header[0] == 7 && len(data) == 0 {
				acknowledged[id] = true
				continue
			}
			if header[0] != 2 {
				continue
			}
			responses[id] = append(responses[id], data...)
			prefix := "A"
			if id%2 == 0 {
				prefix = "B"
			}
			want := fmt.Sprintf("%sflow-%03d", prefix, id)
			if len(responses[id]) >= len(want) {
				if string(responses[id]) != want {
					done <- fmt.Errorf("stream %d data mismatch", id)
					return
				}
				completed[id] = true
			}
		}
		done <- nil
	}()
	for id := uint32(1); id <= 100; id++ {
		if err := writeFrame(c, 1, id, nil); err != nil {
			t.Fatal(err)
		}
		domain := "alpha.test"
		if id%2 == 0 {
			domain = "beta.test"
		}
		var request bytes.Buffer
		if err := M.SocksaddrSerializer.WriteAddrPort(&request, M.Socksaddr{Fqdn: domain, Port: 80}); err != nil {
			t.Fatal(err)
		}
		request.WriteString(fmt.Sprintf("flow-%03d", id))
		if err := writeFrame(c, 2, id, request.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for id := uint32(1); id <= 50; id++ {
		if err := writeFrame(c, 3, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFrame(c, 2, 100, []byte("survivor")); err != nil {
		t.Fatal(err)
	}
	var survivor []byte
	for len(survivor) < len("survivor") {
		var header [7]byte
		if _, err := io.ReadFull(c, header[:]); err != nil {
			t.Fatal("surviving stream closed", err)
		}
		data := make([]byte, binary.BigEndian.Uint16(header[5:]))
		if _, err := io.ReadFull(c, data); err != nil {
			t.Fatal(err)
		}
		if header[0] == 2 && binary.BigEndian.Uint32(header[1:5]) == 100 {
			survivor = append(survivor, data...)
		}
	}
	if string(survivor) != "survivor" {
		t.Fatal("partial close corrupted surviving stream")
	}
	if err := f.alter("mux-user", "", true); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buffer [1024]byte
	for {
		_, err := c.Read(buffer[:])
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("revoked session did not close")
			}
			break
		}
	}
}

func TestAnyTLSFailedAddIsAtomic(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.addInbound(t, f.inbound); err == nil {
		t.Fatal("duplicate tag accepted")
	}
	conflict := make(map[string]any)
	for k, v := range f.inbound {
		conflict[k] = v
	}
	conflict["tag"] = "conflict"
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	conflict["port"] = occupied.Addr().(*net.TCPAddr).Port
	if err := f.addInbound(t, conflict); err == nil {
		t.Fatal("occupied port accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
	if err != nil || len(list.Inbounds) != 1 {
		t.Fatal("failed addition left ghost handler", list, err)
	}
	conflict["port"] = unusedPort(t)
	if err := f.addInbound(t, conflict); err != nil {
		t.Fatal("failed addition leaked tag", err)
	}
}

func TestAnyTLSAuthenticationFailures(t *testing.T) {
	f := newFixture(t, true, true)
	if _, err := exchange(f.client(t, "no-users-yet"), "alpha.test", "reject"); err == nil {
		t.Fatal("empty user table accepted authentication")
	}
	if err := f.alter("alice", "secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(f.client(t, "wrong"), "alpha.test", "reject"); err == nil {
		t.Fatal("wrong password accepted")
	}
	for _, wrongCA := range []bool{false, true} {
		config := f.tls.Clone()
		if wrongCA {
			config.RootCAs = x509.NewCertPool()
		} else {
			config.ServerName = "wrong.test"
		}
		c, err := tls.Dial("tcp", f.address, config)
		if c != nil {
			c.Close()
		}
		if err == nil {
			t.Fatal("untrusted TLS accepted")
		}
	}
}

func TestAnyTLSRPCInvalidConfigAtomicity(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.alter("alice", "secret", false); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"certificate", "settings"} {
		t.Run(invalid, func(t *testing.T) {
			raw, _ := json.Marshal(f.inbound)
			var c conf.InboundDetourConfig
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			cfg, err := c.Build()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Tag = "invalid-" + invalid
			receiverMessage, _ := cfg.ReceiverSettings.GetInstance()
			receiver := receiverMessage.(*proxyman.ReceiverConfig)
			port := unusedPort(t)
			receiver.PortList.Range[0].From = uint32(port)
			receiver.PortList.Range[0].To = uint32(port)
			if invalid == "certificate" {
				security, _ := receiver.StreamSettings.SecuritySettings[0].GetInstance()
				tlsConfig := security.(*xtls.Config)
				tlsConfig.Certificate[0].Key = []byte("private-marker-not-a-key")
				receiver.StreamSettings.SecuritySettings[0] = serial.ToTypedMessage(tlsConfig)
			} else {
				cfg.ProxySettings = serial.ToTypedMessage(&anytls.ServerConfig{MaxSessions: 5000})
			}
			cfg.ReceiverSettings = serial.ToTypedMessage(receiver)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := f.api.AddInbound(ctx, &handler.AddInboundRequest{Inbound: cfg}); err == nil {
				t.Fatal("invalid native RPC configuration accepted")
			} else if strings.Contains(err.Error(), "private-marker") {
				t.Fatal("private material leaked in error")
			}
			list, err := f.api.ListInbounds(ctx, &handler.ListInboundsRequest{})
			if err != nil || len(list.GetInbounds()) != 1 {
				t.Fatal("failed RPC left ghost handler", err)
			}
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatal("failed RPC leaked port", err)
			}
			l.Close()
			if got, err := exchange(f.client(t, "secret"), "alpha.test", "alive"); err != nil || got != "Aalive" {
				t.Fatal("existing listener disrupted", err)
			}
		})
	}
}

func TestAnyTLSResetReleasesSessionQuota(t *testing.T) {
	f := newFixture(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	f.inbound["settings"] = map[string]any{"clients": []any{}, "maxSessionsPerUser": 1}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal(err)
	}
	if err := f.alter("reset", "reset-secret", false); err != nil {
		t.Fatal(err)
	}
	c := rawSession(t, f, "reset-secret")
	var dest bytes.Buffer
	if err := M.SocksaddrSerializer.WriteAddrPort(&dest, M.ParseSocksaddr("alpha.test:80")); err != nil {
		t.Fatal(err)
	}
	dest.WriteString("before-reset")
	if err := writeFrame(c, 1, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(c, 2, 1, dest.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Wait for server output, proving this is an admitted session, then send RST.
	var first [1]byte
	if _, err := c.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	tcp := c.(*tls.Conn).NetConn().(*net.TCPConn)
	if err := tcp.SetLinger(0); err != nil {
		t.Fatal(err)
	}
	tcp.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		client := f.client(t, "reset-secret")
		got, err := exchange(client, "alpha.test", "after-reset")
		client.Close()
		if err == nil && got == "Aafter-reset" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RST leaked the single user session quota", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnyTLSIdleHeartbeatsReleaseQuota(t *testing.T) {
	f := newFixture(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	f.inbound["settings"] = map[string]any{"clients": []any{}, "maxSessionsPerUser": 1}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal(err)
	}
	if err := f.alter("idle", "idle-secret", false); err != nil {
		t.Fatal(err)
	}
	c := rawSession(t, f, "idle-secret")
	c.SetDeadline(time.Now().Add(40 * time.Second))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if writeFrame(c, 8, 0, nil) != nil {
					return
				}
			}
		}
	}()
	defer func() { close(stop); c.Close(); <-done }()
	hearts := 0
	for {
		var header [7]byte
		_, err := io.ReadFull(c, header[:])
		if err != nil {
			if err != io.EOF {
				t.Fatalf("expected idle EOF, got %v", err)
			}
			break
		}
		if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(header[5:]))); err != nil {
			t.Fatal(err)
		}
		if header[0] == 9 {
			hearts++
		}
	}
	if hearts < 10 {
		t.Fatalf("heartbeat handling not exercised: %d", hearts)
	}
	// Quota is returned after the old protocol loop exits; reconnect must work.
	deadline := time.Now().Add(3 * time.Second)
	for {
		client := f.client(t, "idle-secret")
		got, err := exchange(client, "alpha.test", "after-idle")
		client.Close()
		if err == nil && got == "Aafter-idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle quota not released: %v %q", err, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnyTLSStatisticsSwitches(t *testing.T) {
	for _, userEnabled := range []bool{false, true} {
		for _, domainEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("user=%t/domain=%t", userEnabled, domainEnabled), func(t *testing.T) {
				f := newFixture(t, userEnabled, domainEnabled)
				if err := f.alter("switch", "secret", false); err != nil {
					t.Fatal(err)
				}
				if _, err := exchange(f.client(t, "secret"), "alpha.test", "switch"); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, direction := range []string{"uplink", "downlink"} {
					_, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>switch>>>traffic>>>" + direction})
					if (err == nil) != userEnabled {
						t.Fatal("user stats switch not respected", direction, err)
					}
				}
				time.Sleep(1100 * time.Millisecond)
				snapshot, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
				if err != nil || snapshot.Enabled != domainEnabled {
					t.Fatal("domain switch not independent", snapshot, err)
				}
				if domainEnabled && len(snapshot.Buckets) == 0 {
					t.Fatal("missing domain statistics")
				}
			})
		}
	}
}
