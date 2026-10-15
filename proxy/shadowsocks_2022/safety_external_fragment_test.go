package shadowsocks_2022

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
	"golang.org/x/net/proxy"
)

// The observer does not split reads itself. It records the real TCP Read, then
// lets the relay finish publishing its remaining fragments before returning.
type externalFirstReadCounter struct {
	first     chan int
	published <-chan struct{}
	observed  atomic.Bool
	value     atomic.Int64
}

func (c *externalFirstReadCounter) Value() int64      { return c.value.Load() }
func (c *externalFirstReadCounter) Set(n int64) int64 { return c.value.Swap(n) }
func (c *externalFirstReadCounter) Add(n int64) int64 {
	old := c.value.Add(n) - n
	if c.observed.CompareAndSwap(false, true) {
		c.first <- int(n)
		select {
		case <-c.published:
		case <-time.After(time.Second):
			// The receipt assertion separately fails publication over budget.
		}
	}
	return old
}

func externalFragmentListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func externalFragmentClient(t *testing.T, kind, binary, password string, relayPort, socksPort int) {
	t.Helper()
	dir := t.TempDir()
	var cfg any
	if kind == "SINGBOX" {
		cfg = map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socksPort}}, "outbounds": []any{map[string]any{"type": "shadowsocks", "tag": "ss", "server": "127.0.0.1", "server_port": relayPort, "method": MethodAES128GCM, "password": password}}, "route": map[string]any{"final": "ss"}}
	} else {
		cfg = map[string]any{"socks-port": socksPort, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "error", "proxies": []any{map[string]any{"name": "ss", "type": "ss", "server": "127.0.0.1", "port": relayPort, "cipher": MethodAES128GCM, "password": password}}, "rules": []string{"MATCH,ss"}}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path, ready := filepath.Join(dir, "config.json"), filepath.Join(dir, "running")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "-c", path}
	if kind == "MIHOMO" {
		args = []string{"-d", dir, "-f", path, "-post-up", "touch '" + strings.ReplaceAll(ready, "'", "'\"'\"'") + "'"}
	}
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(filepath.Join(dir, "process.log"))
			t.Logf("client log:\n%s", data)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if kind == "MIHOMO" {
			if _, err := os.Stat(ready); err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
		}
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("independent client readiness timeout")
}

// Opt-in until the main agent schedules this low-volume external gate. No
// retries: each identity/control is one independent process and one request.
func TestNativeSSExternalFirstReadContract(t *testing.T) {
	if os.Getenv("XRAY_SS2022_FRAGMENT_INTEROP") != "1" {
		t.Skip("set XRAY_SS2022_FRAGMENT_INTEROP=1 and explicit SS client binaries")
	}
	for _, kind := range []string{"SINGBOX", "MIHOMO"} {
		binary := os.Getenv("XRAY_SS2022_" + kind)
		if binary == "" {
			t.Fatal("required XRAY_SS2022_" + kind)
		}
		for _, identity := range []string{"admin", "user"} {
			for _, fragmented := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/fragmented=%v", kind, identity, fragmented), func(t *testing.T) {
					m := safetyMulti(t) // Original one-second absolute handshake policy.
					defer m.Close()
					var selected []byte
					for i, name := range []string{"admin", "user"} {
						key := bytes.Repeat([]byte{byte(i + 2)}, 16)
						if err := m.AddUser(context.Background(), &protocol.MemoryUser{Email: name, Level: 7, Account: &MemoryAccount{Key: key}}); err != nil {
							t.Fatal(err)
						}
						if name == identity {
							selected = key
						}
					}
					server, relay := externalFragmentListener(t), externalFragmentListener(t)
					reservation := externalFragmentListener(t)
					socksPort := reservation.Addr().(*net.TCPAddr).Port
					reservation.Close()
					first, published := make(chan int, 1), make(chan struct{})
					var publishOnce sync.Once
					release := func() { publishOnce.Do(func() { close(published) }) }
					serverDone, relayDone := make(chan error, 1), make(chan error, 1)
					identities := make(chan string, 2)
					ctx, cancel := context.WithCancel(session.ContextWithInbound(context.Background(), &session.Inbound{}))
					defer cancel()
					r, w := pipe.New(pipe.WithSizeLimit(1 << 20))
					defer r.Interrupt()
					defer w.Interrupt()
					dispatch := safetyDispatcher{call: func(ctx context.Context, dst xnet.Destination) (*transport.Link, error) {
						identities <- session.InboundFromContext(ctx).User.Email
						if dst != xnet.TCPDestination(xnet.LocalHostIP, 12345) {
							return nil, fmt.Errorf("unexpected destination %v", dst)
						}
						return &transport.Link{Reader: r, Writer: w}, nil
					}}
					go func() {
						c, err := server.Accept()
						if err != nil {
							serverDone <- err
							return
						}
						defer c.Close()
						// Use Core's recognized stats wrapper so ResetTCPConn still
						// reaches the real TCP socket and applies SO_LINGER(0).
						serverDone <- m.processTCP(ctx, &stat.CounterConnection{Connection: c, ReadCounter: &externalFirstReadCounter{first: first, published: published}}, dispatch)
					}()
					type receipt struct {
						writes  []int
						read    int
						elapsed time.Duration
					}
					receipts := make(chan receipt, 1)
					wireClose := make(chan error, 1)
					go func() {
						defer release()
						c, err := relay.Accept()
						if err != nil {
							relayDone <- err
							return
						}
						defer c.Close()
						c.SetDeadline(time.Now().Add(4 * time.Second))
						header := make([]byte, 16+AESBlockSize+RequestHeaderFixedChunkLength+AEADTagSize)
						if _, err := io.ReadFull(c, header); err != nil {
							relayDone <- err
							return
						}
						u, err := net.DialTimeout("tcp", server.Addr().String(), time.Second)
						if err != nil {
							relayDone <- err
							return
						}
						defer u.Close()
						u.SetDeadline(time.Now().Add(4 * time.Second))
						start := time.Now()
						rec := receipt{}
						for offset := 0; offset < len(header); {
							size := len(header) - offset
							if fragmented && size > 3 {
								size = 3
							}
							if fragmented && offset == 0 {
								size = 1
							}
							n, e := u.Write(header[offset : offset+size])
							rec.writes = append(rec.writes, n)
							offset += n
							if e != nil {
								receipts <- rec
								relayDone <- e
								return
							}
							if len(rec.writes) == 1 {
								select {
								case rec.read = <-first:
								case <-time.After(time.Second):
									receipts <- rec
									relayDone <- fmt.Errorf("Core first read missing")
									return
								}
							}
						}
						rec.elapsed = time.Since(start)
						receipts <- rec
						// Release the observer before forwarding variable header/payload.
						release()
						copyDone := make(chan struct{})
						go func() { io.Copy(u, c); u.(*net.TCPConn).CloseWrite(); close(copyDone) }()
						replyBytes, readErr := io.Copy(c, u)
						err = readErr
						if fragmented && replyBytes != 0 {
							err = fmt.Errorf("rejected header returned %d wire bytes", replyBytes)
						}
						wireClose <- err
						c.Close()
						u.Close()
						<-copyDone
						relayDone <- err
					}()
					t.Cleanup(func() {
						cancel()
						release()
						r.Interrupt()
						w.Interrupt()
						server.Close()
						relay.Close()
						for _, done := range []chan error{serverDone, relayDone} {
							select {
							case err := <-done:
								t.Logf("joined worker: %v", err)
							case <-time.After(5 * time.Second):
								t.Error("worker did not join")
							}
						}
					})
					password := base64.StdEncoding.EncodeToString(m.masterPSK) + ":" + base64.StdEncoding.EncodeToString(selected)
					externalFragmentClient(t, kind, binary, password, relay.Addr().(*net.TCPAddr).Port, socksPort)
					dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, &net.Dialer{Timeout: time.Second})
					if err != nil {
						t.Fatal(err)
					}
					dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer dialCancel()
					c, err := dialer.(proxy.ContextDialer).DialContext(dialCtx, "tcp", "127.0.0.1:12345")
					if err != nil {
						t.Fatal(err)
					}
					c.SetDeadline(time.Now().Add(3 * time.Second))
					payload := []byte("independent-ss-fragment-" + identity)
					_, exchangeErr := c.Write(payload)
					got := make([]byte, len(payload))
					var replyBytes int
					if exchangeErr == nil {
						replyBytes, exchangeErr = io.ReadFull(c, got)
					}
					c.Close()
					select {
					case rec := <-receipts:
						t.Logf("actual TCP header writes=%v Core-first-read=%d elapsed=%s", rec.writes, rec.read, rec.elapsed)
						if fragmented && (len(rec.writes) < 2 || rec.read != 1) {
							t.Error("fragmentation not actually observed")
						}
						if !fragmented && (len(rec.writes) != 1 || rec.read != 16+AESBlockSize+RequestHeaderFixedChunkLength+AEADTagSize) {
							t.Error("whole-header control did not receive complete first read")
						}
						if rec.elapsed >= time.Second {
							t.Error("original handshake budget exceeded")
						}
					case <-time.After(time.Second):
						t.Error("missing fragmentation receipt")
					}
					if fragmented {
						if replyBytes != 0 {
							t.Errorf("rejected request returned %d plaintext bytes", replyBytes)
						}
						if !(errors.Is(exchangeErr, io.EOF) || errors.Is(exchangeErr, syscall.ECONNRESET) || errors.Is(exchangeErr, syscall.ECONNABORTED) || errors.Is(exchangeErr, syscall.EPIPE)) {
							t.Errorf("short first read must reject, got client error %v", exchangeErr)
						}
						select {
						case err := <-serverDone:
							serverDone <- err
							if err == nil || !strings.Contains(err.Error(), "failed to read complete handshake header") {
								t.Errorf("unexpected rejection: %v", err)
							}
						case <-time.After(time.Second):
							t.Error("short-header handler did not retire")
						}
						select {
						case err := <-wireClose:
							t.Logf("Core-to-relay rejection socket result: %v", err)
							if !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.ECONNABORTED) {
								t.Errorf("want uniform TCP reset, not timeout/plain FIN: %v", err)
							}
						case <-time.After(time.Second):
							t.Error("missing raw TCP reset receipt")
						}
						select {
						case name := <-identities:
							t.Errorf("rejected header dispatched identity %q", name)
						default:
						}
						if session.InboundFromContext(ctx).User != nil {
							t.Error("rejected header published an identity")
						}
						return
					}
					if exchangeErr != nil || !bytes.Equal(got, payload) {
						t.Errorf("exact reply: err=%v got=%q", exchangeErr, got)
					}
					select {
					case gotIdentity := <-identities:
						if gotIdentity != identity {
							t.Errorf("identity=%q want=%q", gotIdentity, identity)
						}
					default:
						t.Error("missing authenticated dispatch identity")
					}
				})
			}
		}
	}
}
