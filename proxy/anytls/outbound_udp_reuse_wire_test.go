package anytls_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/transport"
)

type udpWireUploadReader struct {
	buf.Reader
	pending   [][]byte
	completed chan<- []byte
	ctx       context.Context
}

func (r *udpWireUploadReader) Interrupt() { common.Interrupt(r.Reader) }

func (r *udpWireUploadReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	// buf.Copy calls the next Read only after clientPackets.WriteMultiBuffer
	// returns, including the AnyTLS write watcher join and write-lock release.
	// A received UDP echo alone does not establish that completion ordering.
	for _, packet := range r.pending {
		select {
		case r.completed <- packet:
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
	r.pending = nil
	mb, err := r.Reader.ReadMultiBuffer()
	for _, b := range mb {
		r.pending = append(r.pending, bytes.Clone(b.Bytes()))
	}
	return mb, err
}

type udpWireOutbound struct {
	*chainCountsAlias
	uploaded chan []byte
}

func (h *udpWireOutbound) Dispatch(ctx context.Context, link *transport.Link) {
	h.chainCountsAlias.Dispatch(ctx, &transport.Link{
		Reader: &udpWireUploadReader{Reader: link.Reader, completed: h.uploaded, ctx: ctx},
		Writer: link.Writer,
	})
}

// This TLS tap forwards unmodified AnyTLS plaintext to a verified TLS connection
// to the real Core remote inbound. It neither authenticates nor implements UoT.
// Only the client-facing post-TLS bytes are counted, never both bridge legs.
func udpWireTap(t *testing.T, f *integrationFixture, remote string) *chainCountsPeer {
	t.Helper()
	cert := f.inbound["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["certificates"].([]any)[0].(map[string]any)
	pair, err := tls.X509KeyPair([]byte(strings.Join(cert["certificate"].([]string), "\n")), []byte(strings.Join(cert["key"].([]string), "\n")))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	p := &chainCountsPeer{listener: listener, acceptDone: make(chan struct{}), sessionDone: make(chan struct{}, 16)}
	go func() {
		defer close(p.acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			p.sockets.Store(conn, struct{}{})
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				defer p.sockets.Delete(conn)
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
					t.Error(err)
					return
				}
				upstream, err := (&tls.Dialer{Config: f.tls.Clone()}).DialContext(ctx, "tcp", remote)
				if err != nil {
					t.Error(err)
					return
				}
				p.sockets.Store(upstream, struct{}{})
				defer p.sockets.Delete(upstream)
				defer upstream.Close()
				p.physical.Add(1)
				measured := &chainCountsPlaintextConn{Conn: conn, peer: p}
				done := make(chan struct{}, 2)
				copyAndClose := func(dst, src net.Conn) {
					io.Copy(dst, src)
					conn.Close()
					upstream.Close()
					done <- struct{}{}
				}
				go copyAndClose(upstream, measured)
				go copyAndClose(measured, upstream)
				<-done
				<-done
				p.ended.Add(1)
				p.sessionDone <- struct{}{}
			}()
		}
	}()
	t.Cleanup(func() { p.close(t, true) })
	return p
}

func TestAnyTLSOutboundUDPReuseExactWire(t *testing.T) {
	f, config := outboundFixture(t, "", func(config map[string]any) {
		config["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)["outboundTag"] = "udp-counts-client"
		for _, raw := range config["outbounds"].([]any) {
			out := raw.(map[string]any)
			if out["tag"] == "udp" {
				out["sendThrough"] = "127.0.0.1"
			}
		}
	})
	value, err := config.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := value.(*anytls.ClientConfig)
	peer := udpWireTap(t, f, fmt.Sprintf("127.0.0.1:%d", clientConfig.Server.Port))
	clientConfig.Server.Port = uint32(peer.listener.Addr().(*net.TCPAddr).Port)
	config.ProxySettings = serial.ToTypedMessage(clientConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.AddOutbound(ctx, &handler.AddOutboundRequest{Outbound: config}); err != nil {
		t.Fatal(err)
	}
	manager := f.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	native := manager.GetHandler("client")
	observed := &udpWireOutbound{
		chainCountsAlias: &chainCountsAlias{Handler: native, tag: "udp-counts-client", done: make(chan struct{}, 16)},
		uploaded:         make(chan []byte, 16),
	}
	if err := manager.AddHandler(ctx, observed); err != nil {
		t.Fatal(err)
	}
	readCounter := func(name string) int64 {
		t.Helper()
		r, err := f.stats.GetStats(ctx, &stats.GetStatsRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetStat().GetValue()
	}
	assertWire := func() {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		var previous, got, want [2]int64
		stable := 0
		for {
			before := [2]int64{peer.readBytes.Load(), peer.writtenBytes.Load()}
			got = [2]int64{readCounter("outbound>>>client>>>traffic>>>uplink"), readCounter("outbound>>>client>>>traffic>>>downlink")}
			want = [2]int64{peer.readBytes.Load(), peer.writtenBytes.Load()}
			if before == want && got == want && got == previous && got[0] > 0 && got[1] > 0 {
				stable++
			} else {
				stable = 0
			}
			if stable == 3 {
				return
			}
			previous = got
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatalf("post-TLS wire gRPC=%v peer=%v", got, want)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	wantDomains := map[string][2]uint64{}
	wantUsers := map[string]int64{}
	var logical int64
	for i, user := range []string{"alice", "bob"} {
		if err := f.alter(user, user+"-wire", false); err != nil {
			t.Fatal(err)
		}
		client := f.client(t, user+"-wire")
		for j, domain := range []string{"alpha.test", "beta.test"} {
			packets := openUoT(t, client)
			size := [2][2]int{{1, 8192}, {37, 512}}[i][j]
			for round := range 3 {
				payload := bytes.Repeat([]byte{byte(1 + i*6 + j*3 + round)}, size)
				if err := exchangeUoT(packets, domain, payload); err != nil {
					t.Fatal(err)
				}
				select {
				case uploaded := <-observed.uploaded:
					if !bytes.Equal(uploaded, payload) {
						t.Fatalf("%s/%s round=%d upload completion payload mismatch", user, domain, round)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("%s/%s round=%d native UDP write did not complete", user, domain, round)
				}
			}
			packets.Close()
			obCountsWait(t, observed.done, "native UDP dispatch")
			logical++
			assertWire() // Include asynchronously consumed settings/padding/FIN.
			if observed.opened.Load() != logical || observed.closed.Load() != logical || peer.accepted.Load() != 1 || peer.physical.Load() != 1 || peer.ended.Load() != 0 {
				t.Fatalf("flow=%d dispatch=%d/%d TLS accept/ready/ended=%d/%d/%d", logical, observed.opened.Load(), observed.closed.Load(), peer.accepted.Load(), peer.physical.Load(), peer.ended.Load())
			}
			n := uint64(size * 3)
			wantDomains[user+"/"+domain] = [2]uint64{n, n}
			r := wantDomains["remote/"+domain]
			wantDomains["remote/"+domain] = [2]uint64{r[0] + n, r[1] + n}
			wantUsers[user] += int64(n)
			wantUsers["remote"] += int64(n)
		}
	}
	for user, n := range wantUsers {
		for _, direction := range []string{"uplink", "downlink"} {
			if got := readCounter("user>>>" + user + ">>>traffic>>>" + direction); got != n {
				t.Fatalf("logical %s/%s=%d want=%d", user, direction, got, n)
			}
		}
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		r, err := f.stats.GetDomainTrafficBuckets(ctx, &stats.GetDomainTrafficBucketsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string][2]uint64{}
		for _, bucket := range r.Buckets {
			for _, entry := range bucket.Entries {
				if _, ok := wantUsers[entry.User]; !ok {
					continue
				}
				key := entry.User + "/" + entry.Domain
				v := got[key]
				got[key] = [2]uint64{v[0] + entry.UplinkBytes, v[1] + entry.DownlinkBytes}
			}
		}
		if reflect.DeepEqual(got, wantDomains) {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("domain got=%v want=%v", got, wantDomains)
		}
	}
	assertWire()
	if peer.readBytes.Load() <= wantUsers["remote"] || peer.writtenBytes.Load() <= wantUsers["remote"] {
		t.Fatal("wire oracle omitted framing overhead")
	}
	t.Logf("12 verified UDP round trips, 4 completed native logical dispatches, 1 reused physical TLS session; exact payload=%v; exact post-TLS wire=%d/%d", wantUsers, peer.readBytes.Load(), peer.writtenBytes.Load())
	if _, err := f.api.RemoveOutbound(ctx, &handler.RemoveOutboundRequest{Tag: "client"}); err != nil {
		t.Fatal(err)
	}
	obCountsWait(t, native.(outbound.RetiringHandler).Retirement().Retire(), "native UDP pool retirement")
	obCountsWait(t, peer.sessionDone, "TLS bridge copy workers")
	peer.close(t, false)
	if peer.ended.Load() != 1 {
		t.Fatal("physical session did not close exactly once")
	}
}
