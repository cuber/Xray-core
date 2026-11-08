package hysteria_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/core"
	hyproxy "github.com/xtls/xray-core/proxy/hysteria"
	hytransport "github.com/xtls/xray-core/transport/internet/hysteria"
)

// Core spec025 FR-002 to FR-005 on started instances built from JSON: what each end sends with and without
// hysteriaSettings.disablePadding, read by raw Hy2 peers, and round trips between real ends in every
// combination.

type paddingPKI struct {
	ca, cert, key []string
	pair          tls.Certificate
	roots         *x509.CertPool
}

func newPaddingPKI(t *testing.T) *paddingPKI {
	t.Helper()
	p := &paddingPKI{}
	p.ca, p.cert, p.key = relayPKI(t)
	pair, err := tls.X509KeyPair([]byte(strings.Join(p.cert, "\n")), []byte(strings.Join(p.key, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	p.pair, p.roots = pair, x509.NewCertPool()
	if !p.roots.AppendCertsFromPEM([]byte(strings.Join(p.ca, "\n"))) {
		t.Fatal("CA")
	}
	return p
}

func (p *paddingPKI) clientTLS() map[string]any {
	return map[string]any{
		"alpn": []string{"h3"}, "serverName": "relay.test", "disableSystemRoot": true,
		"certificates": []any{map[string]any{"usage": "verify", "certificate": p.ca}},
	}
}

func (p *paddingPKI) serverTLS() map[string]any {
	return map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificate": p.cert, "key": p.key}}}
}

// hysteriaSettings is a Hy2 end's settings: disablePadding written only when off, unset otherwise.
func hysteriaSettings(auth string, off bool) map[string]any {
	s := map[string]any{"version": 2}
	if auth != "" {
		s["auth"] = auth
	}
	if off {
		s["disablePadding"] = true
	}
	return s
}

func startInstance(t *testing.T, config map[string]any) {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
}

// rawStream is one TCP stream a raw server read: its connection, frame type, relay index and padding.
type rawStream struct {
	conn         int
	frame, index uint64
	padding      string
}

// rawHy2Server authenticates every client, answers each relay index ok, reads each stream's TCP request with its
// padding, answers it and echoes.
type rawHy2Server struct {
	port    int
	mu      sync.Mutex
	auths   []http.Header // the first authentication of each connection, in accept order
	streams []rawStream
}

func newRawHy2Server(t *testing.T, pki *paddingPKI) *rawHy2Server {
	t.Helper()
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := quic.Listen(packet, &tls.Config{Certificates: []tls.Certificate{pki.pair}, NextProtos: []string{http3.NextProtoH3}},
		&quic.Config{EnableDatagrams: true, MaxIdleTimeout: 30 * time.Second})
	if err != nil {
		packet.Close()
		t.Fatal(err)
	}
	s := &rawHy2Server{port: packet.LocalAddr().(*net.UDPAddr).Port}
	var conns []*quic.Conn
	var workers sync.WaitGroup
	t.Cleanup(func() {
		listener.Close()
		s.mu.Lock()
		for _, c := range conns {
			c.CloseWithError(0, "")
		}
		s.mu.Unlock()
		workers.Wait()
		packet.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			s.mu.Lock()
			conns = append(conns, conn)
			id := len(conns)
			s.mu.Unlock()
			var authenticated atomic.Bool
			server := &http3.Server{EnableDatagrams: true}
			server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if authenticated.CompareAndSwap(false, true) {
					s.mu.Lock()
					s.auths = append(s.auths, r.Header.Clone())
					s.mu.Unlock()
				}
				if relays := r.Header.Get(hytransport.HeaderRelay); relays != "" {
					var answers []string
					for _, entry := range strings.Split(relays, ",") {
						index, _, _ := strings.Cut(entry, "=")
						answers = append(answers, index+"=ok:"+base64.RawURLEncoding.EncodeToString([]byte("r"+index+"@relay.test")))
					}
					w.Header().Set(hytransport.HeaderRelay, strings.Join(answers, ","))
				}
				w.Header().Set(hytransport.ResponseHeaderUDPEnabled, "false")
				w.Header().Set(hytransport.CommonHeaderCCRX, "0")
				w.WriteHeader(hytransport.StatusAuthOK)
			})
			server.StreamDispatcher = func(frame http3.FrameType, stream *quic.Stream, err error) (bool, error) {
				if err != nil || !authenticated.Load() || frame != hytransport.FrameTypeTCPRequest && frame != hytransport.FrameTypeTCPRelayRequest {
					return false, err
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					s.serve(t, id, stream)
				}()
				return true, nil
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				server.ServeQUICConn(conn)
			}()
		}
	}()
	return s
}

func (s *rawHy2Server) serve(t *testing.T, conn int, stream *quic.Stream) {
	defer stream.Close()
	defer stream.CancelRead(0)
	stream.SetDeadline(time.Now().Add(10 * time.Second))
	r := quicvarint.NewReader(stream)
	read := func() uint64 {
		v, err := quicvarint.Read(r)
		if err != nil {
			t.Errorf("raw server: %v", err)
		}
		return v
	}
	got := rawStream{conn: conn, frame: read()}
	if got.frame == hytransport.FrameTypeTCPRelayRequest {
		got.index = read()
	}
	addr := make([]byte, read())
	if _, err := io.ReadFull(stream, addr); err != nil {
		return
	}
	padding := make([]byte, read())
	if _, err := io.ReadFull(stream, padding); err != nil {
		return
	}
	got.padding = string(padding)
	s.mu.Lock()
	s.streams = append(s.streams, got)
	s.mu.Unlock()
	if err := hyproxy.WriteTCPResponse(stream, true, "", true); err != nil {
		return
	}
	stream.SetDeadline(time.Time{})
	io.Copy(stream, stream)
}

func (s *rawHy2Server) recorded() ([]http.Header, []rawStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.auths...), append([]rawStream(nil), s.streams...)
}

// door is a dokodemo-door inbound on a free port that sends its connections to outbound tag.
func door(t *testing.T, tag, network string, ports map[string]int) (map[string]any, map[string]any) {
	ports[network+"-"+tag] = relayFreePort(t, network)
	inbound := map[string]any{
		"tag": network + "-door-" + tag, "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": ports[network+"-"+tag],
		"settings": map[string]any{"address": "127.0.0.1", "port": 9, "network": network},
	}
	rule := map[string]any{"type": "field", "inboundTag": []string{network + "-door-" + tag}, "outboundTag": tag}
	return inbound, rule
}

func hy2Outbound(tag string, port int, pki *paddingPKI, settings map[string]any) map[string]any {
	return map[string]any{
		"tag": tag, "protocol": "hysteria", "sendThrough": "127.0.0.1",
		"settings": map[string]any{"version": 2, "address": "127.0.0.1", "port": port},
		"streamSettings": map[string]any{
			"network": "hysteria", "security": "tls", "tlsSettings": pki.clientTLS(), "hysteriaSettings": settings,
			"finalmask": map[string]any{"quicParams": map[string]any{"keepAlivePeriod": 10}},
		},
	}
}

// startPaddingClient starts a client whose outbound out, session owner owner (up to two connections, the second
// dialed as soon as one stream is open) and owner's relay outbound relay reach port; it returns the TCP doors.
func startPaddingClient(t *testing.T, pki *paddingPKI, port int, off bool) map[string]int {
	t.Helper()
	owner := hysteriaSettings("secret-owner", off)
	owner["session"] = map[string]any{"minConnections": 1, "maxConnections": 2, "streamsPerConnection": 16, "scaleUpStreams": 1}
	ports := map[string]int{}
	var inbounds, rules []any
	for _, tag := range []string{"out", "owner", "relay"} {
		in, rule := door(t, tag, "tcp", ports)
		inbounds, rules = append(inbounds, in), append(rules, rule)
	}
	startInstance(t, map[string]any{
		"log":      map[string]any{"loglevel": "error", "access": "none"},
		"inbounds": inbounds,
		"outbounds": []any{
			hy2Outbound("out", port, pki, hysteriaSettings("secret-out", off)),
			hy2Outbound("owner", port, pki, owner),
			map[string]any{"tag": "relay", "protocol": "hysteria", "settings": map[string]any{
				"version": 2, "relay": map[string]any{"session": "owner", "auth": "secret-relay"},
			}},
		},
		"routing": map[string]any{"rules": rules},
	})
	return ports
}

// echoThrough opens a TCP connection to port, checks that a message comes back after prefix, and returns the
// connection open.
func echoThrough(t *testing.T, port int, prefix string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(prefix)+4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != prefix+"ping" {
		t.Fatalf("through port %d: %q, %v", port, got, err)
	}
	conn.SetDeadline(time.Time{})
	return conn
}

// checkSent fails unless every padding follows the end's setting: none when off, else in [min, max).
func checkSent(t *testing.T, what string, off bool, paddings []string, low, high int) {
	t.Helper()
	if len(paddings) == 0 {
		t.Fatalf("%s: nothing recorded", what)
	}
	for _, p := range paddings {
		switch {
		case off && p != "":
			t.Errorf("%s with disablePadding: %d characters of padding", what, len(p))
		case !off && (len(p) < low || len(p) >= high):
			t.Errorf("%s: %d characters of padding, want %d to %d", what, len(p), low, high-1)
		}
	}
}

// FR-002, FR-003 (client end), FR-005: a client built from JSON sends its authentication requests and TCP
// requests as its setting says, read by a raw server. A session owner's streams and its relay outbound's 0x402
// streams follow the owner on every connection of its pool.
func TestClientSendsPaddingAsConfigured(t *testing.T) {
	pki := newPaddingPKI(t)
	for _, off := range []bool{false, true} {
		t.Run(fmt.Sprintf("disablePadding=%v", off), func(t *testing.T) {
			server := newRawHy2Server(t, pki)
			doors := startPaddingClient(t, pki, server.port, off)
			echoThrough(t, doors["tcp-out"], "").Close()
			held := []net.Conn{echoThrough(t, doors["tcp-owner"], "")}
			defer func() {
				for _, c := range held {
					c.Close()
				}
			}()
			// The owner's second connection, dialed because one stream is open.
			deadline := time.Now().Add(5 * time.Second)
			for auths, _ := server.recorded(); len(auths) < 3; auths, _ = server.recorded() {
				if time.Now().After(deadline) {
					t.Fatalf("connections authenticated: %d, want 3", len(auths))
				}
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			for _, tag := range []string{"relay", "owner", "relay", "relay"} {
				held = append(held, echoThrough(t, doors["tcp-"+tag], ""))
			}

			auths, streams := server.recorded()
			var authPaddings []string
			relayRequests := 0
			for _, h := range auths {
				authPaddings = append(authPaddings, h.Get(hytransport.CommonHeaderPadding))
				if _, sent := h[hytransport.CommonHeaderPadding]; off && sent {
					t.Errorf("an authentication request with disablePadding carries %s", hytransport.CommonHeaderPadding)
				}
				if h.Get(hytransport.HeaderRelay) != "" {
					relayRequests++
				}
			}
			checkSent(t, "authentication requests", off, authPaddings, 256, 2048)
			if relayRequests != 2 {
				t.Errorf("the owner's connections negotiating relays: %d, want 2", relayRequests)
			}
			byFrame := map[uint64][]string{}
			ownerConns := map[int]bool{}
			for _, s := range streams {
				byFrame[s.frame] = append(byFrame[s.frame], s.padding)
				if s.conn != 1 {
					ownerConns[s.conn] = true
				}
				if s.frame == hytransport.FrameTypeTCPRelayRequest && s.index != 1 {
					t.Errorf("relay index %d", s.index)
				}
			}
			if len(byFrame[hytransport.FrameTypeTCPRequest]) != 3 || len(byFrame[hytransport.FrameTypeTCPRelayRequest]) != 3 {
				t.Fatalf("streams %v", streams)
			}
			checkSent(t, "TCP requests", off, byFrame[hytransport.FrameTypeTCPRequest], 64, 512)
			checkSent(t, "relayed 0x402 requests", off, byFrame[hytransport.FrameTypeTCPRelayRequest], 64, 512)
			if len(ownerConns) != 2 {
				t.Errorf("the owner's streams ran on %d of its 2 connections: %v", len(ownerConns), streams)
			}
		})
	}
}

// rawHy2Client authenticates to a Hy2 server twice on one connection and opens TCP streams on it, reading what
// the server sends.
type rawHy2Client struct {
	rt   *http3.Transport
	conn *quic.Conn
}

func newRawHy2Client(pki *paddingPKI, port int) *rawHy2Client {
	c := &rawHy2Client{}
	c.rt = &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pki.roots, ServerName: "relay.test", NextProtos: []string{http3.NextProtoH3}},
		QUICConfig:      &quic.Config{EnableDatagrams: true},
		Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			conn, err := quic.DialAddrEarly(ctx, "127.0.0.1:"+strconv.Itoa(port), tlsCfg, cfg)
			c.conn = conn
			return conn, err
		},
	}
	return c
}

func (c *rawHy2Client) authenticate(t *testing.T, auth string) http.Header {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := (&http.Request{
		Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: hytransport.URLHost, Path: hytransport.URLPath},
		Header: http.Header{hytransport.RequestHeaderAuth: []string{auth}},
	}).WithContext(ctx)
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != hytransport.StatusAuthOK {
		t.Fatalf("authentication: %d", resp.StatusCode)
	}
	return resp.Header
}

// request opens a TCP stream and returns the padding of the server's response; the echo behind it starts with mark.
func (c *rawHy2Client) request(t *testing.T, mark string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := c.conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write(quicvarint.Append(nil, hytransport.FrameTypeTCPRequest)); err != nil {
		t.Fatal(err)
	}
	if err := hyproxy.WriteTCPRequest(stream, "127.0.0.1:9", true); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	r := quicvarint.NewReader(stream)
	status := make([]byte, 1)
	if _, err := io.ReadFull(stream, status); err != nil || status[0] != 0 {
		t.Fatalf("response status %v, %v", status, err)
	}
	msg, err := quicvarint.Read(r)
	if err != nil || msg != 0 {
		t.Fatalf("response message %d, %v", msg, err)
	}
	n, err := quicvarint.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	padding := make([]byte, n)
	if _, err := io.ReadFull(stream, padding); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(mark)+4)
	if _, err := io.ReadFull(stream, echo); err != nil || string(echo) != mark+"ping" {
		t.Fatalf("echo %q, %v", echo, err)
	}
	stream.CancelRead(0)
	return string(padding)
}

// startPaddingServer starts a Hy2 server whose user secret-x reaches an echo marked E; it returns its port.
func startPaddingServer(t *testing.T, pki *paddingPKI, off bool) int {
	t.Helper()
	port := relayFreePort(t, "udp")
	settings := hysteriaSettings("", off)
	startInstance(t, map[string]any{
		"log": map[string]any{"loglevel": "error", "access": "none"},
		"inbounds": []any{map[string]any{
			"tag": "hy2-in", "protocol": "hysteria", "listen": "127.0.0.1", "port": port,
			"settings":       map[string]any{"version": 2, "clients": []any{map[string]any{"auth": "secret-x", "email": "x@relay.test"}}},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": pki.serverTLS(), "hysteriaSettings": settings},
		}},
		"outbounds": []any{map[string]any{
			"tag": "echo", "protocol": "freedom", "sendThrough": "127.0.0.1",
			"settings": map[string]any{
				"redirect":   fmt.Sprintf("127.0.0.1:%d", relayEcho(t, "E")),
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
			},
		}},
	})
	return port
}

// FR-002, FR-003 (server end): a server built from JSON answers the first and a repeated authentication and every
// TCP request as its setting says, read by a raw client.
func TestServerSendsPaddingAsConfigured(t *testing.T) {
	pki := newPaddingPKI(t)
	for _, off := range []bool{false, true} {
		t.Run(fmt.Sprintf("disablePadding=%v", off), func(t *testing.T) {
			client := newRawHy2Client(pki, startPaddingServer(t, pki, off))
			defer client.rt.Close()
			var auths, responses []string
			for range 2 {
				h := client.authenticate(t, "secret-x")
				if _, sent := h[hytransport.CommonHeaderPadding]; off && sent {
					t.Errorf("an authentication answer with disablePadding carries %s", hytransport.CommonHeaderPadding)
				}
				auths = append(auths, h.Get(hytransport.CommonHeaderPadding))
			}
			for range 5 {
				responses = append(responses, client.request(t, "E"))
			}
			checkSent(t, "authentication answers", off, auths, 256, 2048)
			checkSent(t, "TCP responses", off, responses, 128, 1024)
		})
	}
}

// FR-003, FR-004: TCP and UDP round trips between a real client and a real server, with the field at neither end,
// at one end and at both.
func TestPaddingEndsInteroperate(t *testing.T) {
	pki := newPaddingPKI(t)
	for _, c := range []struct{ client, server bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("client=%v,server=%v", c.client, c.server), func(t *testing.T) {
			port := startPaddingServer(t, pki, c.server)
			ports := map[string]int{}
			tcpIn, tcpRule := door(t, "out", "tcp", ports)
			udpIn, udpRule := door(t, "out", "udp", ports)
			startInstance(t, map[string]any{
				"log":       map[string]any{"loglevel": "error", "access": "none"},
				"inbounds":  []any{tcpIn, udpIn},
				"outbounds": []any{hy2Outbound("out", port, pki, hysteriaSettings("secret-x", c.client))},
				"routing":   map[string]any{"rules": []any{tcpRule, udpRule}},
			})
			for range 3 {
				echoThrough(t, ports["tcp-out"], "E").Close()
			}
			f := &relayFixture{doors: map[string]int{"udp-out": ports["udp-out"]}}
			for range 3 {
				if got, err := f.datagram("out"); err != nil || got != "E" {
					t.Fatalf("UDP: %q, %v", got, err)
				}
			}
		})
	}
}
