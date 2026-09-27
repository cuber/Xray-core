package dns

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	mdns "github.com/miekg/dns"
	utls "github.com/refraction-networking/utls"
	dnsfeature "github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/testing/servers/dnsfixture"
)

type wireDNSFixture struct{ queries atomic.Int32 }

// Own an IPv4 loopback socket for the local fixture. The optional diagnostic
// retains the wildcard allocator used by quic.DialAddr and 64 occupied ports.
// quic.Dial creates its single-use Transport with zero-length client CIDs,
// unlike a default public Transport.Dial. Keep the concrete UDPConn intact so
// QUIC retains ReadMsgUDP/WriteMsgUDP and its normal OOB-capable socket path.
func fixtureQUICDialer(t *testing.T, server net.Addr, diagnostic bool) func(context.Context, string, *tls.Config, *quic.Config) (*quic.Conn, error) {
	t.Helper()
	started := time.Now()
	var mu sync.Mutex
	var records []string
	var sockets []*net.UDPConn
	var connections []*quic.Conn
	var dials, readers, closers sync.WaitGroup
	closed := false
	occupied := make(map[int]*net.UDPConn)
	record := func(format string, args ...any) {
		mu.Lock()
		records = append(records, fmt.Sprintf("%s %s", time.Since(started), fmt.Sprintf(format, args...)))
		mu.Unlock()
	}
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		// The normal nameserver cleanup runs first and waits for getConnection.
		// Seal registration too, so late callers cannot escape fixture ownership.
		dials.Wait()
		for _, connection := range connections {
			_ = connection.CloseWithError(0, "fixture complete")
		}
		for _, socket := range sockets {
			_ = socket.Close()
		}
		closers.Wait()
		for _, socket := range occupied {
			_ = socket.Close()
		}
		readers.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, line := range records {
			t.Log(line)
		}
	})
	occupiedCount := 0
	if diagnostic {
		occupiedCount = 64
	}
	for range occupiedCount {
		socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		occupied[socket.LocalAddr().(*net.UDPAddr).Port] = socket
		readers.Add(1)
		go func() {
			defer readers.Done()
			packet := make([]byte, 65535)
			for {
				n, source, err := socket.ReadFromUDP(packet)
				if err != nil {
					return
				}
				record("OWNED-IPv4-RECEIVE local=%s source=%s bytes=%d prefix=%x", socket.LocalAddr(), source, n, packet[:min(n, 48)])
			}
		}()
	}
	record("server=%s owned-loopback-listeners=%d", server, len(occupied))
	return func(ctx context.Context, address string, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return nil, net.ErrClosed
		}
		dials.Add(1)
		mu.Unlock()
		defer dials.Done()
		network, bindIP := "udp4", net.IPv4(127, 0, 0, 1)
		if diagnostic {
			// Match quic-go/client.go DialAddr's wildcard allocation.
			network, bindIP = "udp", net.IPv4zero
		}
		socket, err := net.ListenUDP(network, &net.UDPAddr{IP: bindIP})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		sockets = append(sockets, socket)
		mu.Unlock()
		remote, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			socket.Close()
			return nil, err
		}
		local := socket.LocalAddr()
		record("DIAL local=%s remote=%s socketType=%T context=%v", local, remote, socket, ctx.Err())
		if blocker := occupied[local.(*net.UDPAddr).Port]; blocker != nil {
			record("COLLISION wildcard=%s existingOwnedIPv4=%s remote=%s", local, blocker.LocalAddr(), remote)
		}
		connection, err := quic.Dial(ctx, socket, remote, tlsConfig, config)
		record("DIAL-RESULT local=%s remote=%s error=%v", local, remote, err)
		if err != nil {
			socket.Close()
			return nil, err
		}
		mu.Lock()
		connections = append(connections, connection)
		mu.Unlock()
		// DialAddr owns its UDP socket; quic.Dial does not. Preserve that
		// ownership explicitly without changing cancellation or retry policy.
		closers.Add(1)
		go func() {
			defer closers.Done()
			<-connection.Context().Done()
			socket.Close()
		}()
		return connection, nil
	}
}

func (f *wireDNSFixture) response(query *mdns.Msg) []byte {
	f.queries.Add(1)
	response := new(mdns.Msg).SetReply(query)
	for _, question := range query.Question {
		switch question.Name {
		case "malformed.test.":
			return []byte{0}
		case "timeout.test.":
			return nil
		case "missing.test.":
			response.Rcode = mdns.RcodeNameError
		default:
			header := mdns.RR_Header{Name: question.Name, Rrtype: question.Qtype, Class: mdns.ClassINET, Ttl: 300}
			switch question.Qtype {
			case mdns.TypeA:
				response.Answer = append(response.Answer, &mdns.A{Hdr: header, A: net.ParseIP("192.0.2.1")})
			case mdns.TypeAAAA:
				response.Answer = append(response.Answer, &mdns.AAAA{Hdr: header, AAAA: net.ParseIP("2001:db8::1")})
			}
		}
	}
	data, _ := response.Pack()
	return data
}

func (f *wireDNSFixture) ServeDNS(w mdns.ResponseWriter, query *mdns.Msg) {
	if response := f.response(query); response != nil {
		_, _ = w.Write(response)
	}
}

func fixtureClient(t *testing.T, protocol string, f *wireDNSFixture) Server {
	t.Helper()
	switch protocol {
	case "tcp":
		address := dnsfixture.Start(t, "tcp", f)
		u, _ := url.Parse("tcp+local://" + address)
		s, err := NewTCPLocalNameServer(u, false, false, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.cacheController.cacheCleanup.Close() })
		return s
	case "doh":
		cert, roots := dnsfixture.Certificate(t)
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 {
				t.Error("DoH did not use HTTP/2")
			}
			data, err := io.ReadAll(io.LimitReader(r.Body, 65536))
			if err != nil {
				return
			}
			query := new(mdns.Msg)
			if err := query.Unpack(data); err != nil {
				t.Error(err)
				return
			}
			response := f.response(query)
			if response == nil {
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(response)
		}))
		server.EnableHTTP2 = true
		server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		server.StartTLS()
		t.Cleanup(server.Close)
		u, _ := url.Parse(server.URL + "/dns-query")
		s := newDoHNameServer(u, nil, false, false, false, 0, nil, &utls.Config{RootCAs: roots})
		t.Cleanup(func() { s.httpClient.CloseIdleConnections(); _ = s.cacheController.cacheCleanup.Close() })
		return s
	case "quic":
		cert, roots := dnsfixture.Certificate(t)
		listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"doq"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				conn, err := listener.Accept(ctx)
				if err != nil {
					return
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer conn.CloseWithError(0, "fixture closed")
					for {
						stream, err := conn.AcceptStream(ctx)
						if err != nil {
							return
						}
						workers.Add(1)
						go func() {
							defer workers.Done()
							defer stream.Close()
							_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
							var length uint16
							if err := binary.Read(stream, binary.BigEndian, &length); err != nil {
								return
							}
							data := make([]byte, length)
							if _, err := io.ReadFull(stream, data); err != nil {
								return
							}
							query := new(mdns.Msg)
							if err := query.Unpack(data); err != nil {
								t.Error(err)
								return
							}
							response := f.response(query)
							if response == nil {
								<-ctx.Done()
								return
							}
							_ = binary.Write(stream, binary.BigEndian, uint16(len(response)))
							_, _ = stream.Write(response)
						}()
					}
				}()
			}
		}()
		t.Cleanup(func() { cancel(); _ = listener.Close(); workers.Wait() })
		u, _ := url.Parse("quic://" + listener.Addr().String())
		s, err := newQUICNameServer(u, false, false, 0, nil, &tls.Config{RootCAs: roots})
		if err != nil {
			t.Fatal(err)
		}
		mode := os.Getenv("XRAY_DNS_QUIC_DIAGNOSTIC")
		if mode != "" && mode != "wildcard64" {
			t.Fatalf("unknown XRAY_DNS_QUIC_DIAGNOSTIC mode %q", mode)
		}
		s.dial = fixtureQUICDialer(t, listener.Addr(), mode == "wildcard64")
		t.Cleanup(func() {
			s.Lock()
			defer s.Unlock()
			if s.connection != nil {
				_ = s.connection.CloseWithError(0, "test complete")
			}
			_ = s.cacheController.cacheCleanup.Close()
		})
		return s
	default:
		t.Fatalf("unknown protocol %q", protocol)
		return nil
	}
}

func TestLocalDNSTransports(t *testing.T) {
	for _, protocol := range []string{"tcp", "doh", "quic"} {
		t.Run(protocol, func(t *testing.T) {
			for _, family := range []struct {
				name   string
				option dnsfeature.IPOption
				want   []string
			}{
				{"ipv4", dnsfeature.IPOption{IPv4Enable: true}, []string{"192.0.2.1"}},
				{"ipv6", dnsfeature.IPOption{IPv6Enable: true}, []string{"2001:db8::1"}},
				{"dual", dnsfeature.IPOption{IPv4Enable: true, IPv6Enable: true}, []string{"192.0.2.1", "2001:db8::1"}},
			} {
				t.Run(family.name, func(t *testing.T) {
					f := new(wireDNSFixture)
					s := fixtureClient(t, protocol, f)
					for attempt := 0; attempt < 2; attempt++ {
						// Responses are published before the cache write completes.
						// Wait for that write before asserting a cache-only lookup.
						if attempt == 1 {
							deadline := time.Now().Add(time.Second)
							for {
								r := s.(CachedNameserver).getCacheController().findRecords("fixture.test.")
								if r != nil && (!family.option.IPv4Enable || r.A != nil) && (!family.option.IPv6Enable || r.AAAA != nil) {
									break
								}
								if time.Now().After(deadline) {
									t.Fatal("cache write did not complete")
								}
								time.Sleep(time.Millisecond)
							}
						}
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						ips, _, err := s.QueryIP(ctx, "fixture.test", family.option)
						cancel()
						if err != nil {
							t.Fatal(err)
						}
						got := make([]string, len(ips))
						for i, ip := range ips {
							got[i] = ip.String()
						}
						sort.Strings(got)
						if !reflect.DeepEqual(got, family.want) {
							t.Fatalf("got %v, want %v", got, family.want)
						}
						if got := f.queries.Load(); got != int32(len(family.want)) {
							t.Fatalf("cache attempt %d: wire queries = %d", attempt, got)
						}
					}
				})
			}
			for _, domain := range []string{"missing.test", "malformed.test", "timeout.test"} {
				t.Run(domain, func(t *testing.T) {
					f := new(wireDNSFixture)
					s := fixtureClient(t, protocol, f)
					ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
					defer cancel()
					ips, _, err := s.QueryIP(ctx, domain, dnsfeature.IPOption{IPv4Enable: true})
					if err == nil || len(ips) != 0 {
						t.Fatalf("got %v, %v; want failure without IPs", ips, err)
					}
					if f.queries.Load() == 0 {
						t.Fatal("fixture never received query")
					}
				})
			}
		})
	}
}

func TestLocalNameServerFixture(t *testing.T) {
	f := new(wireDNSFixture)
	address := dnsfixture.Start(t, "udp", f)
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	ips, _, err := NewLocalNameServer().QueryIP(context.Background(), "fixture.test.", dnsfeature.IPOption{IPv4Enable: true, IPv6Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(ips))
	for i, ip := range ips {
		got[i] = ip.String()
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"192.0.2.1", "2001:db8::1"}) {
		t.Fatalf("unexpected addresses: %v", got)
	}
}
