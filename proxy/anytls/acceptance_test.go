package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
)

func TestAnyTLSRepeatedConnectionsAndListenerClose(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.alter("cleanup", "cleanup-secret", false); err != nil {
		t.Fatal(err)
	}
	before := openFDCount(t)
	for round := range 100 {
		client := f.client(t, "cleanup-secret")
		if got, err := exchange(client, "alpha.test", "cleanup"); err != nil || got != "Acleanup" {
			t.Fatalf("round %d: %q %v", round, got, err)
		}
		client.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	for {
		after := openFDCount(t)
		if before < 0 || after <= before+3 {
			t.Logf("FDs before=%d after=%d", before, after)
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("FDs not reclaimed: before=%d after=%d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnyTLSInboundStatsDisabled(t *testing.T) {
	f := newFixture(t, true, true, func(config map[string]any) {
		config["policy"].(map[string]any)["system"] = map[string]any{}
	})
	if err := f.alter("stat-switch", "stat-secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(f.client(t, "stat-secret"), "alpha.test", "stats"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, direction := range []string{"uplink", "downlink"} {
		if _, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "inbound>>>anytls-test>>>traffic>>>" + direction}); err == nil {
			t.Fatal("disabled inbound counter created")
		}
		if _, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>stat-switch>>>traffic>>>" + direction}); err != nil {
			t.Fatal("inbound switch disabled user counter", err)
		}
	}
}

func TestAnyTLSPaddingAndIPAccounting(t *testing.T) {
	for _, padded := range []bool{false, true} {
		t.Run(fmt.Sprintf("padded=%t", padded), func(t *testing.T) {
			f := newFixture(t, true, true)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if padded {
				if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
					t.Fatal(err)
				}
				f.inbound["settings"].(map[string]any)["paddingScheme"] = []string{"stop=8", "0=4096-4096", "1=4096-4096"}
				if err := f.addInbound(t, f.inbound); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.alter("padding-test", "padding-test-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "padding-test-secret")
			for _, domain := range []string{"alpha.test", "192.0.2.1"} {
				conn, err := client.DialContext(ctx, M.ParseSocksaddr(domain+":80"))
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				_, err = conn.Write([]byte("abc"))
				var reply [4]byte
				if err == nil {
					_, err = io.ReadFull(conn, reply[:])
				}
				conn.Close()
				if err != nil || string(reply[:]) != "Aabc" {
					t.Fatal("padding changed business payload", err)
				}
			}
			for direction, expected := range map[string]int64{"uplink": 6, "downlink": 8} {
				value, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>padding-test>>>traffic>>>" + direction})
				if err != nil || value.Stat.GetValue() != expected {
					t.Fatal("padding or IP flow counted incorrectly", direction, err)
				}
			}
			time.Sleep(1100 * time.Millisecond)
			buckets, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			totals := map[string][2]uint64{}
			for _, bucket := range buckets.Buckets {
				for _, entry := range bucket.Entries {
					if entry.User != "padding-test" || (entry.Domain != "alpha.test" && entry.Domain != "unknown") {
						t.Fatal("pure IP flow created a fabricated domain entry")
					}
					previous := totals[entry.Domain]
					totals[entry.Domain] = [2]uint64{previous[0] + entry.UplinkBytes, previous[1] + entry.DownlinkBytes}
				}
			}
			if totals["alpha.test"] != [2]uint64{3, 4} || totals["unknown"] != [2]uint64{3, 4} {
				t.Fatalf("domain/unknown payload mismatch: %v", totals)
			}
		})
	}
}

func TestAnyTLSNetworkAdmissionAndHandshakeTimeout(t *testing.T) {
	f := newFixture(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	f.inbound["settings"] = map[string]any{"clients": []any{}, "maxSessions": 2, "maxSessionsPerUser": 1, "maxStreamsPerSession": 2}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"alice", "bob", "charlie"} {
		if err := f.alter(user, user+"-quota-secret", false); err != nil {
			t.Fatal(err)
		}
	}
	alice := f.client(t, "alice-quota-secret")
	if _, err := exchange(alice, "alpha.test", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(f.client(t, "alice-quota-secret"), "alpha.test", "denied"); err == nil {
		t.Fatal("per-user session quota bypassed")
	}
	bob := f.client(t, "bob-quota-secret")
	if got, err := exchange(bob, "alpha.test", "two"); err != nil || got != "Btwo" {
		t.Fatal("another user's quota was consumed", err)
	}
	if _, err := exchange(f.client(t, "charlie-quota-secret"), "alpha.test", "global-limit"); err == nil {
		t.Fatal("global session quota bypassed")
	}
	if _, err := exchange(alice, "alpha.test", "still-alive"); err != nil {
		t.Fatal("quota rejection killed the admitted session", err)
	}
	alice.Close()
	bob.Close()
	// Removing the listener waits for every handler, so the timeout check starts
	// with no previously draining sessions and a deterministic empty registry.
	if _, err := f.api.RemoveInbound(ctx, &handler.RemoveInboundRequest{Tag: "anytls-test"}); err != nil {
		t.Fatal(err)
	}
	if err := f.addInbound(t, f.inbound); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.Dial("tcp", f.address, f.tls)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	var byteBuffer [1]byte
	_, err = conn.Read(byteBuffer[:])
	if err == nil {
		t.Fatal("unauthenticated connection returned application data")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("server did not enforce the authentication deadline")
	}
}

type countedTLSConn struct {
	net.Conn
	uplink, downlink *atomic.Int64
}

func (c *countedTLSConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.downlink.Add(int64(n))
	return n, err
}

func (c *countedTLSConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.uplink.Add(int64(n))
	return n, err
}

func TestAnyTLSInboundCounterBoundary(t *testing.T) {
	f := newFixture(t, true, true)
	if err := f.alter("boundary", "boundary-secret", false); err != nil {
		t.Fatal(err)
	}
	var up, down atomic.Int64
	client, err := engine.NewClient(engine.ClientOptions{
		Password: "boundary-secret",
		DialOut: func(ctx context.Context) (net.Conn, error) {
			conn, err := (&tls.Dialer{Config: f.tls}).DialContext(ctx, "tcp", f.address)
			if err != nil {
				return nil, err
			}
			return &countedTLSConn{Conn: conn, uplink: &up, downlink: &down}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, domain := range []string{"alpha.test", "beta.test"} {
		if _, err := exchange(client, domain, "boundary-payload"); err != nil {
			t.Fatal(err)
		}
	}
	// Leave the idle session open so neither endpoint sends a close frame while
	// comparing the identical post-TLS (not TCP wire) byte boundary.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for direction, expected := range map[string]*atomic.Int64{"uplink": &up, "downlink": &down} {
		for {
			result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "inbound>>>anytls-test>>>traffic>>>" + direction})
			if err != nil {
				t.Fatal(err)
			}
			if result.Stat.Value == expected.Load() && expected.Load() > 0 {
				break
			}
			if ctx.Err() != nil {
				t.Fatalf("%s boundary mismatch: core=%d client=%d", direction, result.Stat.Value, expected.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestAnyTLSExactLargePayload(t *testing.T) {
	upload := bytes.Repeat([]byte{0x39}, 1<<20)
	download := bytes.Repeat([]byte{0xb4}, 2<<20)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		data := make([]byte, len(upload))
		if _, err = io.ReadFull(conn, data); err == nil && !bytes.Equal(data, upload) {
			err = fmt.Errorf("upload mismatch")
		}
		if err == nil {
			_, err = io.Copy(conn, bytes.NewReader(download))
		}
		served <- err
	}()
	f := newFixture(t, true, true, func(config map[string]any) {
		config["outbounds"].([]any)[0].(map[string]any)["settings"] = map[string]any{"redirect": listener.Addr().String(), "ipsBlocked": []string{}}
	})
	if err := f.alter("bulk", "bulk-test-secret", false); err != nil {
		t.Fatal(err)
	}
	client := f.client(t, "bulk-test-secret")
	conn, err := client.DialContext(context.Background(), M.Socksaddr{Fqdn: "bulk.test", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.Copy(conn, bytes.NewReader(upload)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(download))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, download) {
		t.Fatal("download mismatch", err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for direction, expected := range map[string]int64{"uplink": int64(len(upload)), "downlink": int64(len(download))} {
		result, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: "user>>>bulk>>>traffic>>>" + direction})
		if err != nil || result.Stat.GetValue() != expected {
			t.Fatalf("%s: want %d, got %v (%v)", direction, expected, result, err)
		}
	}
}

func TestAnyTLSUoTRejectsUnsupportedSize(t *testing.T) {
	for _, size := range []int{0, 8193, 65507} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newFixture(t, true, true)
			if err := f.alter("udp", "udp-size-test", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "udp-size-test")
			conn, err := client.DialContext(context.Background(), M.Socksaddr{Fqdn: uot.MagicAddress})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			request := uot.Request{Destination: M.ParseSocksaddr("192.0.2.1:53")}
			if err := uot.WriteRequest(conn, request); err != nil {
				t.Fatal(err)
			}
			packets := uot.NewConn(conn, request)
			if err := packets.WritePacket(B.As(make([]byte, size)), request.Destination); err != nil {
				t.Fatal(err)
			}
			response := B.NewSize(65535)
			defer response.Release()
			_, err = packets.ReadPacket(response)
			if err == nil {
				t.Fatal("unsupported payload accepted")
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("unsupported payload was not rejected promptly", err)
			}
		})
	}
}

func TestAnyTLSUoTIndependentIPv4IPv6Targets(t *testing.T) {
	backend := func(network, address, marker string) string {
		conn, err := net.ListenPacket(network, address)
		if err != nil {
			t.Fatal("isolated UDP listener", err)
		}
		t.Cleanup(func() { conn.Close() })
		go func() {
			data := make([]byte, 8192)
			for {
				n, from, err := conn.ReadFrom(data)
				if err != nil {
					return
				}
				conn.WriteTo(append([]byte(marker), data[:n]...), from)
			}
		}()
		return conn.LocalAddr().String()
	}
	a := backend("udp4", "127.0.0.1:0", "A")
	b := backend("udp6", "[::1]:0", "B")
	f := newFixture(t, true, true, func(config map[string]any) {
		config["outbounds"] = append(config["outbounds"].([]any),
			map[string]any{"tag": "udp-a", "protocol": "freedom", "settings": map[string]any{"redirect": a, "ipsBlocked": []string{}}},
			map[string]any{"tag": "udp-b", "protocol": "freedom", "settings": map[string]any{"redirect": b, "ipsBlocked": []string{}}})
		config["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "network": "udp", "domain": []string{"full:udp-a.test"}, "outboundTag": "udp-a"},
			map[string]any{"type": "field", "network": "udp", "outboundTag": "udp-b"},
		}}
	})
	if err := f.alter("udp-targets", "udp-target-test", false); err != nil {
		t.Fatal(err)
	}
	conn, err := f.client(t, "udp-target-test").DialContext(context.Background(), M.Socksaddr{Fqdn: uot.MagicAddress})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	request := uot.Request{Destination: M.ParseSocksaddr("192.0.2.1:53")}
	if err := uot.WriteRequest(conn, request); err != nil {
		t.Fatal(err)
	}
	packets := uot.NewConn(conn, request)
	for i := range 10 {
		dest := M.Socksaddr{Fqdn: "udp-a.test", Port: 53}
		want := "Apacket"
		if i%2 == 1 {
			dest = M.ParseSocksaddr("[2001:db8::1]:53")
			want = "Bpacket"
		}
		if err := packets.WritePacket(B.As([]byte("packet")), dest); err != nil {
			t.Fatal(err)
		}
		response := B.NewSize(8192)
		from, err := packets.ReadPacket(response)
		got := string(response.Bytes())
		response.Release()
		if err != nil || got != want || from != dest {
			t.Fatalf("target %d: got %q want %q from=%v want=%v (%v)", i, got, want, from, dest, err)
		}
	}
}
