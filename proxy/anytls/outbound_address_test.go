package anytls_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/app/proxyman"
	handler "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/anytls"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/transport/internet"
)

// Serial only: Core's DNS/outbound manager and system dialer are process-global.
// These tests cover static binding, not interface/address changes during reuse.
func addressCapability(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("ANYTLS_STRICT") == "1" {
		t.Fatalf("required address capability unavailable: %v", err)
	}
	t.Skipf("address capability unavailable: %v", err)
}

func addressJoin(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("address fixture worker did not stop")
	}
}

// Every listener and accepted socket is closed before joining its owned workers.
func addressEcho(t *testing.T, network, host string) string {
	t.Helper()
	done := make(chan struct{})
	if network == "udp6" {
		c, err := net.ListenPacket(network, net.JoinHostPort(host, "0"))
		addressCapability(t, err)
		go func() {
			defer close(done)
			b := make([]byte, 65535)
			for {
				n, peer, err := c.ReadFrom(b)
				if err != nil {
					return
				}
				if _, err := c.WriteTo(b[:n], peer); err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() { c.Close(); addressJoin(t, done) })
		return c.LocalAddr().String()
	}
	l, err := net.Listen(network, net.JoinHostPort(host, "0"))
	addressCapability(t, err)
	var mu sync.Mutex
	var sockets []net.Conn
	var workers sync.WaitGroup
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			sockets = append(sockets, c)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(15 * time.Second))
				io.Copy(c, struct{ io.Reader }{c})
			}()
		}
	}()
	t.Cleanup(func() {
		l.Close()
		addressJoin(t, done)
		mu.Lock()
		for _, c := range sockets {
			c.Close()
		}
		mu.Unlock()
		joined := make(chan struct{})
		go func() { workers.Wait(); close(joined) }()
		addressJoin(t, joined)
	})
	return l.Addr().String()
}

func addressReplace(t *testing.T, f *integrationFixture, c *core.OutboundHandlerConfig, modify func(*anytls.ClientConfig, *proxyman.SenderConfig)) {
	t.Helper()
	proxy, err := c.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := c.SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	modify(proxy.(*anytls.ClientConfig), sender.(*proxyman.SenderConfig))
	c.ProxySettings, c.SenderSettings = serial.ToTypedMessage(proxy), serial.ToTypedMessage(sender)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: c.Tag}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: c}); err != nil {
		t.Fatal(err)
	}
}

func addressTCP(c *engine.Client, target M.Socksaddr, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := c.DialContext(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("TCP payload mismatch")
	}
	return nil
}

func TestAnyTLSOutboundAddressIPv6(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/socks", "dialerProxy/socks"} {
		t.Run("chain="+mode, func(t *testing.T) {
			tcp := addressEcho(t, "tcp6", "::1")
			udp := []string{addressEcho(t, "udp6", "::1"), addressEcho(t, "udp6", "::1")}
			f, config := outboundFixture(t, mode, func(c map[string]any) {
				c["outbounds"] = append(c["outbounds"].([]any), map[string]any{"tag": "address-v6", "protocol": "freedom", "sendThrough": "::1", "settings": map[string]any{"ipsBlocked": []string{}}})
				r := c["routing"].(map[string]any)
				r["rules"] = append([]any{map[string]any{"type": "field", "inboundTag": []string{"anytls-v6"}, "outboundTag": "address-v6"}}, r["rules"].([]any)...)
			})
			// A second real IPv6 AnyTLS listener, not an IPv4 redirect to a v6 target.
			l, err := net.Listen("tcp6", "[::1]:0")
			addressCapability(t, err)
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			remote := map[string]any{}
			for k, v := range f.inbound {
				remote[k] = v
			}
			remote["tag"], remote["listen"], remote["port"] = "anytls-v6", "::1", port
			remote["settings"] = map[string]any{"clients": []any{map[string]any{"email": "remote-v6", "password": "remote-secret"}}}
			if err := f.addInbound(t, remote); err != nil {
				t.Fatal(err)
			}
			addressReplace(t, f, config, func(c *anytls.ClientConfig, _ *proxyman.SenderConfig) {
				c.Server.Address = xnet.NewIPOrDomain(xnet.ParseAddress("::1"))
				c.Server.Port = uint32(port)
			})
			if err := f.alter("alice", "address-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "address-secret")
			if err := addressTCP(client, M.ParseSocksaddr(tcp), bytes.Repeat([]byte("v6"), 4096)); err != nil {
				t.Fatal(err)
			}
			packets := openUoT(t, client)
			defer packets.Close()
			for _, size := range []int{1, 8192} {
				for index, target := range udp {
					payload := bytes.Repeat([]byte{byte(index + size)}, size)
					dest := M.ParseSocksaddr(target)
					if err := packets.WritePacket(B.As(payload), dest); err != nil {
						t.Fatal(err)
					}
					b := B.NewSize(65535)
					from, err := packets.ReadPacket(b)
					matches := bytes.Equal(b.Bytes(), payload)
					b.Release()
					if err != nil || from != dest || !matches {
						t.Fatalf("IPv6 UDP size=%d: from=%v want=%v exact=%v err=%v", size, from, dest, matches, err)
					}
				}
			}
			t.Log("real ::1 AnyTLS transport and TCP/UDP destinations; two UDP ports in one association, 1/8192 exact bytes and reply endpoints")
		})
	}
}

func addressDNS(t *testing.T) (string, func() map[string]int) {
	t.Helper()
	c, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	queries := map[string]int{}
	ready, done := make(chan struct{}), make(chan struct{})
	s := &dns.Server{Listener: c, NotifyStartedFunc: func() { close(ready) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		for _, question := range q.Question {
			mu.Lock()
			queries[question.Name]++
			mu.Unlock()
			if (question.Name == "server.address.test." || question.Name == "relay.address.test.") && question.Qtype == dns.TypeA {
				r.Answer = append(r.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("127.0.0.1")})
			} else {
				r.Rcode = dns.RcodeNameError
			}
		}
		w.WriteMsg(r)
	})}
	go func() { defer close(done); s.ActivateAndServe() }()
	t.Cleanup(func() { s.Shutdown(); c.Close(); addressJoin(t, done) })
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("DNS readiness timeout")
	}
	return c.Addr().String(), func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		result := map[string]int{}
		for k, v := range queries {
			result[k] = v
		}
		return result
	}
}

func TestAnyTLSOutboundAddressDNSLocality(t *testing.T) {
	for _, mode := range []string{"", "proxySettings/socks", "dialerProxy/socks"} {
		t.Run("chain="+mode, func(t *testing.T) {
			resolver, queries := addressDNS(t)
			f, config := outboundFixture(t, mode, func(c map[string]any) {
				c["dns"] = map[string]any{"servers": []string{"tcp+local://" + resolver}, "queryStrategy": "UseIPv4", "disableCache": true}
				// Only the remote inbound can select the domain route. All other
				// remote traffic is rejected, so an early IP substitution cannot pass.
				c["outbounds"] = append(c["outbounds"].([]any), map[string]any{"tag": "address-reject", "protocol": "blackhole"})
				r := c["routing"].(map[string]any)
				r["domainStrategy"] = "AsIs"
				r["rules"] = append([]any{
					map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "domain": []string{"full:payload.address.test"}, "network": "tcp", "outboundTag": "b"},
					map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "domain": []string{"full:payload.address.test"}, "network": "udp", "outboundTag": "udp"},
					map[string]any{"type": "field", "inboundTag": []string{"anytls-remote"}, "outboundTag": "address-reject"},
				}, r["rules"].([]any)...)
				if mode != "" {
					hopRule := map[string]any{"type": "field", "inboundTag": []string{"hop-in"}, "outboundTag": "chain-exit"}
					if mode == "proxySettings/socks" {
						hopRule["domain"] = []string{"full:server.address.test"}
					} else {
						hopRule["ip"] = []string{"127.0.0.1/32"}
					}
					r["rules"] = append([]any{
						hopRule,
						map[string]any{"type": "field", "inboundTag": []string{"hop-in"}, "outboundTag": "address-reject"},
					}, r["rules"].([]any)...)
				}
				c["outbounds"].([]any)[2].(map[string]any)["sendThrough"] = "127.0.0.1"
				// proxySettings resolves the AnyTLS endpoint at the SOCKS exit;
				// dialerProxy resolves it before dispatching to the SOCKS hop.
				for _, ob := range c["outbounds"].([]any) {
					v := ob.(map[string]any)
					if v["tag"] == "chain-exit" {
						v["settings"].(map[string]any)["domainStrategy"] = "ForceIPv4"
					}
				}
			})
			if mode != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				list, err := f.api.ListOutbounds(ctx, &handler.ListOutboundsRequest{})
				if err != nil {
					t.Fatal(err)
				}
				var hop *core.OutboundHandlerConfig
				for _, outbound := range list.Outbounds {
					if outbound.Tag == "hop" {
						hop = outbound
						break
					}
				}
				if hop == nil {
					t.Fatal("SOCKS hop missing from fixture")
				}
				proxy, err := hop.ProxySettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				proxy.(*socks.ClientConfig).Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress("relay.address.test"))
				hop.ProxySettings = serial.ToTypedMessage(proxy)
				sender, err := hop.SenderSettings.GetInstance()
				if err != nil {
					t.Fatal(err)
				}
				s := sender.(*proxyman.SenderConfig)
				if s.StreamSettings == nil {
					s.StreamSettings = &internet.StreamConfig{}
				}
				s.StreamSettings.SocketSettings = &internet.SocketConfig{DomainStrategy: internet.DomainStrategy_FORCE_IP4}
				hop.SenderSettings = serial.ToTypedMessage(s)
				if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: hop.Tag}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: hop}); err != nil {
					t.Fatal(err)
				}
			}
			addressReplace(t, f, config, func(c *anytls.ClientConfig, s *proxyman.SenderConfig) {
				c.Server.Address = xnet.NewIPOrDomain(xnet.DomainAddress("server.address.test"))
				if s.StreamSettings.SocketSettings == nil {
					s.StreamSettings.SocketSettings = &internet.SocketConfig{}
				}
				s.StreamSettings.SocketSettings.DomainStrategy = internet.DomainStrategy_FORCE_IP4
			})
			if err := f.alter("alice", "address-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "address-secret")
			if got, err := exchange(client, "payload.address.test", "domain"); err != nil || got != "Bdomain" {
				t.Fatalf("remote domain-only TCP route: %q %v", got, err)
			}
			packets := openUoT(t, client)
			defer packets.Close()
			if err := exchangeUoT(packets, "payload.address.test", []byte("domain-udp")); err != nil {
				t.Fatal(err)
			}
			got := queries()
			wantNames := 1
			if mode != "" {
				wantNames = 2
				if got["relay.address.test."] == 0 {
					t.Fatalf("SOCKS relay hostname was not resolved: queries=%v", got)
				}
			}
			if got["server.address.test."] == 0 || got["payload.address.test."] != 0 || len(got) != wantNames {
				t.Fatalf("DNS locality: queries=%v", got)
			}
			t.Logf("controlled DNS queries=%v; TCP/UDP payload domain reached remote domain-only routes unchanged; CA verified with independent SNI anytls.test", got)
		})
	}
}

type addressSocketObservation struct {
	local  string
	source string
	iface  string
	err    error
}
type addressSystemDialer struct {
	internet.DefaultSystemDialer
	port     xnet.Port
	observed chan addressSocketObservation
}

func (d *addressSystemDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	c, err := d.DefaultSystemDialer.Dial(ctx, source, dest, opts)
	if err == nil && dest.Port == d.port && dest.Network == xnet.Network_TCP {
		o := addressSocketObservation{local: c.LocalAddr().(*net.TCPAddr).IP.String()}
		if source != nil {
			o.source = source.String()
		}
		if opts != nil && opts.Interface != "" {
			o.iface, o.err = addressBoundInterface(c)
		}
		d.observed <- o
	}
	return c, err
}

func TestAnyTLSOutboundAddressStaticBinding(t *testing.T) {
	for _, binding := range []string{"source", "source-alias", "interface"} {
		t.Run(binding, func(t *testing.T) {
			iface := ""
			if binding == "interface" {
				interfaces, err := net.Interfaces()
				addressCapability(t, err)
				for _, i := range interfaces {
					if i.Flags&net.FlagLoopback != 0 && i.Flags&net.FlagUp != 0 {
						iface = i.Name
						break
					}
				}
				if iface == "" {
					addressCapability(t, fmt.Errorf("no up loopback interface"))
				}
			}
			// Verify availability before product assertions. Socket-option errors
			// are logged, not propagated, by Core's DefaultSystemDialer.
			probe := addressEcho(t, "tcp4", "127.0.0.1")
			source := "127.0.0.1"
			if binding == "source-alias" {
				source = "127.0.0.2"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := (&internet.DefaultSystemDialer{}).Dial(ctx, xnet.ParseAddress(source), xnet.TCPDestination(xnet.ParseAddress("127.0.0.1"), xnet.Port(M.ParseSocksaddr(probe).Port)), &internet.SocketConfig{Interface: iface})
			addressCapability(t, err)
			if iface != "" {
				got, err := addressBoundInterface(c)
				c.Close()
				addressCapability(t, err)
				if got != iface {
					addressCapability(t, fmt.Errorf("kernel binding=%q want=%q", got, iface))
				}
			} else {
				c.Close()
			}
			d := &addressSystemDialer{observed: make(chan addressSocketObservation, 8)}
			internet.UseAlternativeSystemDialer(d)
			t.Cleanup(func() { internet.UseAlternativeSystemDialer(nil) })
			f, config := outboundFixture(t, "")
			addressReplace(t, f, config, func(c *anytls.ClientConfig, s *proxyman.SenderConfig) {
				d.port = xnet.Port(c.Server.Port)
				s.Via = xnet.NewIPOrDomain(xnet.ParseAddress(source))
				s.StreamSettings.SocketSettings = &internet.SocketConfig{Interface: iface}
			})
			if err := f.alter("alice", "address-secret", false); err != nil {
				t.Fatal(err)
			}
			client := f.client(t, "address-secret")
			if binding == "source" {
				addressReplace(t, f, config, func(_ *anytls.ClientConfig, s *proxyman.SenderConfig) {
					s.Via = xnet.NewIPOrDomain(xnet.ParseAddress("::1"))
				})
				if got, err := exchange(client, "alpha.test", "must-not-ignore-source"); err == nil {
					t.Fatalf("IPv6 source silently ignored for IPv4 endpoint: %q", got)
				}
				addressReplace(t, f, config, func(_ *anytls.ClientConfig, s *proxyman.SenderConfig) {
					s.Via = xnet.NewIPOrDomain(xnet.ParseAddress(source))
				})
			}
			if got, err := exchange(client, "alpha.test", "bound"); err != nil || got != "Abound" {
				t.Fatalf("bound AnyTLS exchange: %q %v", got, err)
			}
			select {
			case got := <-d.observed:
				if got.err != nil || got.source != source || got.local != source || got.iface != iface {
					t.Fatalf("physical AnyTLS socket=%+v want source=%s interface=%s", got, source, iface)
				}
				t.Logf("physical AnyTLS socket: source=%s local=%s kernel interface=%q", got.source, got.local, got.iface)
			case <-time.After(5 * time.Second):
				t.Fatal("no physical AnyTLS socket observation")
			}
		})
	}
}
