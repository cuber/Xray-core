package anytls_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
)

// The same race-instrumented test executable runs one real Core per process.
// No Core instance or global dialer is initialized in the parent process.
func TestAnyTLSProcessHelper(t *testing.T) {
	path := os.Getenv("ANYTLS_PROCESS_CONFIG")
	if path == "" {
		t.Skip("subprocess helper")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	x, err := core.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeCore := func() {
		closeOnce.Do(func() {
			timer := time.NewTimer(3 * time.Second)
			stop, joined := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(joined)
				select {
				case <-stop:
				case <-timer.C:
					stack := make([]byte, 4<<20)
					n := runtime.Stack(stack, true)
					fmt.Fprintf(os.Stderr, "Core Close blocked for 3s; pid=%d\n%s\n", os.Getpid(), stack[:n])
				}
			}()
			defer func() { timer.Stop(); close(stop); <-joined }()
			if err := x.Close(); err != nil {
				t.Errorf("Core Close: %v", err)
			}
		})
	}
	defer closeCore()
	if err := os.WriteFile(path+".ready", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	closeCore()
}

type anyTLSCoreProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}
	err     error
	logPath string
	once    sync.Once
}

func startAnyTLSCoreProcess(t *testing.T, config map[string]any) *anyTLSCoreProcess {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "core.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	recordAnyTLSFixture(t, "core-process", raw)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &anyTLSCoreProcess{done: make(chan struct{}), logPath: filepath.Join(dir, "core.log")}
	p.cmd = exec.Command(executable, "-test.run=^TestAnyTLSProcessHelper$", "-test.timeout=3m")
	p.cmd.Env = append(os.Environ(), "ANYTLS_PROCESS_CONFIG="+path)
	log, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stdout, p.cmd.Stderr = log, log
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	if err = p.cmd.Start(); err != nil {
		p.stdin.Close()
		log.Close()
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); log.Close(); close(p.done) }()
	t.Cleanup(func() { p.stop(t) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			data, _ := os.ReadFile(p.logPath)
			t.Fatalf("Core helper exited: %v\n%s", p.err, data)
		case <-deadline.C:
			t.Fatal("Core helper readiness timeout")
		case <-tick.C:
			if ready, err := os.ReadFile(path + ".ready"); err == nil {
				if string(ready) != strconv.Itoa(p.cmd.Process.Pid) || p.cmd.Process.Pid == os.Getpid() {
					t.Fatal("invalid independent process identity")
				}
				t.Logf("Core ready pid=%d config=%s", p.cmd.Process.Pid, path)
				return p
			}
		}
	}
}

func (p *anyTLSCoreProcess) stop(t *testing.T) {
	t.Helper()
	p.once.Do(func() {
		p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			p.cmd.Process.Kill()
			t.Error("Core did not close within 5s; killed")
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Error("Core process could not be joined")
				return
			}
		}
		if p.err != nil {
			data, _ := os.ReadFile(p.logPath)
			t.Errorf("Core exit: %v\n%s", p.err, data)
		}
		t.Logf("Core joined pid=%d", p.cmd.Process.Pid)
	})
}

type anyTLSProcessBackend struct {
	tcp    net.Listener
	udp    net.PacketConn
	mu     sync.Mutex
	counts map[string]int
	held   chan struct{}
	stopCh chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
}

func (b *anyTLSProcessBackend) stop(t *testing.T) {
	t.Helper()
	b.once.Do(func() {
		close(b.stopCh)
		b.tcp.Close()
		b.udp.Close()
		done := make(chan struct{})
		go func() { b.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Fatal("backend workers not joined; final receipt accounting is invalid")
		}
	})
}

func newAnyTLSProcessBackend(t *testing.T) *anyTLSProcessBackend {
	t.Helper()
	b := &anyTLSProcessBackend{counts: map[string]int{}, held: make(chan struct{}, 1), stopCh: make(chan struct{})}
	var err error
	b.tcp, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.udp, err = net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		b.tcp.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { b.stop(t) })
	receipt := func(payload []byte) { b.mu.Lock(); b.counts[string(payload)]++; b.mu.Unlock() }
	b.wg.Add(2)
	go func() {
		defer b.wg.Done()
		for {
			c, err := b.tcp.Accept()
			if err != nil {
				return
			}
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				var n uint32
				if binary.Read(c, binary.BigEndian, &n) != nil || n > 65535 {
					return
				}
				payload := make([]byte, n)
				if _, err := io.ReadFull(c, payload); err != nil {
					return
				}
				receipt(payload)
				if bytes.HasPrefix(payload, []byte("held:")) {
					select {
					case b.held <- struct{}{}:
					default:
					}
					<-b.stopCh
					return
				}
				response := append([]byte("tcp@"+b.tcp.Addr().String()+":"), payload...)
				if binary.Write(c, binary.BigEndian, uint32(len(response))) != nil {
					return
				}
				c.Write(response)
			}()
		}
	}()
	go func() {
		defer b.wg.Done()
		buffer := make([]byte, 65535)
		for {
			n, peer, err := b.udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			payload := buffer[:n]
			receipt(payload)
			b.udp.WriteTo(append([]byte("udp@"+b.udp.LocalAddr().String()+":"), payload...), peer)
		}
	}()
	return b
}

func TestAnyTLSOutboundSeparateProcesses(t *testing.T) {
	modes := []string{"direct"}
	for _, protocol := range []string{"anytls", "socks", "vless", "hysteria"} {
		for _, entrance := range []string{"proxySettings", "dialerProxy"} {
			modes = append(modes, entrance+"/"+protocol)
		}
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			cert, key, ca, _ := externalControlCertificate(t, t.TempDir())
			backend := newAnyTLSProcessBackend(t)
			serverTLS := map[string]any{"certificates": []any{map[string]any{"certificateFile": cert, "keyFile": key}}}
			clientTLS := map[string]any{"serverName": "anytls.test", "certificates": []any{map[string]any{"usage": "verify", "certificateFile": ca}}}
			inbound := func(port int) map[string]any {
				return map[string]any{
					"tag": "in", "listen": "127.0.0.1", "port": port, "protocol": "anytls",
					"settings":       map[string]any{"clients": []any{map[string]any{"email": "process-user", "password": "process-secret"}}},
					"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": serverTLS},
				}
			}
			outbound := func(tag string, port int) map[string]any {
				return map[string]any{
					"tag": tag, "protocol": "anytls", "settings": map[string]any{"address": "127.0.0.1", "port": port, "password": "process-secret"},
					"streamSettings": map[string]any{"network": "raw", "security": "tls", "tlsSettings": clientTLS},
				}
			}
			base := func(ins, outs []any) map[string]any {
				return map[string]any{
					"log": map[string]any{"loglevel": "error"}, "inbounds": ins, "outbounds": outs,
					"policy": map[string]any{"levels": map[string]any{"0": map[string]any{"handshake": 2, "connIdle": 150, "uplinkOnly": 1, "downlinkOnly": 1}}},
				}
			}
			free := func() map[string]any {
				return map[string]any{"protocol": "freedom", "sendThrough": "127.0.0.1", "settings": map[string]any{"ipsBlocked": []string{}}}
			}
			remotePort := unusedTCPUDPPort(t)
			remoteConfig := base([]any{inbound(remotePort)}, []any{free()})
			remote := startAnyTLSCoreProcess(t, remoteConfig)
			primary := outbound("remote", remotePort)
			outs := []any{primary}
			var relay *anyTLSCoreProcess
			var relayConfig map[string]any
			if mode != "direct" {
				entrance, protocol, _ := strings.Cut(mode, "/")
				relayPort := unusedTCPUDPPort(t)
				relayIn, relayOut := inbound(relayPort), outbound("relay", relayPort)
				relayIn["protocol"], relayOut["protocol"] = protocol, protocol
				switch protocol {
				case "socks":
					delete(relayIn, "streamSettings")
					delete(relayOut, "streamSettings")
					relayIn["settings"] = map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "hop", "pass": "process-secret"}}}
					relayOut["settings"] = map[string]any{"address": "127.0.0.1", "port": relayPort, "user": "hop", "pass": "process-secret"}
				case "vless":
					const id = "db96e722-a410-4e18-8d16-e33f4e5d74db"
					delete(relayIn, "streamSettings")
					delete(relayOut, "streamSettings")
					relayIn["settings"] = map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id, "email": "hop"}}}
					relayOut["settings"] = map[string]any{"address": "127.0.0.1", "port": relayPort, "id": id, "encryption": "none"}
				case "hysteria":
					relayIn["settings"] = map[string]any{"version": 2, "clients": []any{map[string]any{"auth": "process-secret", "email": "hop"}}}
					relayOut["settings"] = map[string]any{"address": "127.0.0.1", "port": relayPort, "version": 2}
					relayOut["sendThrough"] = "127.0.0.1"
					relayIn["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "hysteriaSettings": map[string]any{"version": 2}, "tlsSettings": map[string]any{"alpn": []string{"h3"}, "certificates": serverTLS["certificates"]}}
					relayOut["streamSettings"] = map[string]any{"network": "hysteria", "security": "tls", "hysteriaSettings": map[string]any{"version": 2, "auth": "process-secret"}, "tlsSettings": map[string]any{"alpn": []string{"h3"}, "serverName": "anytls.test", "certificates": clientTLS["certificates"]}}
				}
				relayConfig = base([]any{relayIn}, []any{free()})
				relay = startAnyTLSCoreProcess(t, relayConfig)
				outs = append(outs, relayOut)
				if entrance == "proxySettings" {
					primary["proxySettings"] = map[string]any{"tag": "relay"}
				} else {
					primary["streamSettings"].(map[string]any)["sockopt"] = map[string]any{"dialerProxy": "relay"}
				}
			}
			entryTCP, entryUDP := unusedTCPUDPPort(t), unusedTCPUDPPort(t)
			doko := func(port int, network, target string) map[string]any {
				host, p, _ := net.SplitHostPort(target)
				targetPort, _ := strconv.Atoi(p)
				return map[string]any{"tag": network, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": host, "port": targetPort, "network": network}}
			}
			entry := startAnyTLSCoreProcess(t, base([]any{doko(entryTCP, "tcp", backend.tcp.Addr().String()), doko(entryUDP, "udp", backend.udp.LocalAddr().String())}, outs))
			requestCtx, cancelRequests := context.WithCancel(context.Background())
			var requestWorkers sync.WaitGroup
			t.Cleanup(func() {
				cancelRequests()
				done := make(chan struct{})
				go func() { requestWorkers.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("parent request workers did not join")
				}
			})
			payload := func(id string) []byte { return append([]byte(id+":"), bytes.Repeat([]byte{0, 1, 127, 255}, 512)...) }
			request := func(network, id string) error {
				port, target := entryTCP, backend.tcp.Addr().String()
				if network == "udp" {
					port, target = entryUDP, backend.udp.LocalAddr().String()
				}
				c, err := net.DialTimeout(network+"4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
				if err != nil {
					return err
				}
				defer c.Close()
				stop := context.AfterFunc(requestCtx, func() { c.Close() })
				defer stop()
				budget := 4 * time.Second
				if id == "held:relay" {
					// Match the derived pool-cleanup + QUIC-idle bound used by
					// the in-process close-barrier fixture, not a recovery sleep.
					budget = 100 * time.Second
				}
				c.SetDeadline(time.Now().Add(budget))
				data := payload(id)
				if network == "tcp" {
					if err := binary.Write(c, binary.BigEndian, uint32(len(data))); err != nil {
						return err
					}
				}
				if _, err := c.Write(data); err != nil {
					return err
				}
				want := append([]byte(network+"@"+target+":"), data...)
				got := make([]byte, len(want)+1)
				var n int
				if network == "tcp" {
					var length uint32
					if err := binary.Read(c, binary.BigEndian, &length); err != nil {
						return err
					}
					if length != uint32(len(want)) {
						return fmt.Errorf("TCP frame length=%d want=%d", length, len(want))
					}
					n, err = io.ReadFull(c, got[:len(want)])
				} else {
					n, err = c.Read(got)
				}
				if err != nil {
					return err
				}
				if !bytes.Equal(got[:n], want) {
					return fmt.Errorf("wrong %s endpoint/payload: got %d want %d bytes", network, n, len(want))
				}
				return nil
			}
			for _, network := range []string{"tcp", "udp"} {
				if err := request(network, "before-"+network); err != nil {
					t.Fatal(err)
				}
			}
			if relay != nil {
				relayInterrupted := make(chan error, 1)
				requestWorkers.Add(1)
				go func() {
					defer requestWorkers.Done()
					relayInterrupted <- request("tcp", "held:relay")
				}()
				select {
				case <-backend.held:
				case <-time.After(5 * time.Second):
					t.Fatal("relay held receipt not observed")
				}
				relay.stop(t)
				select {
				case err := <-relayInterrupted:
					if err == nil {
						t.Fatal("relay interrupted request succeeded")
					}
					if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
						t.Fatal("local timeout is not a relay protocol-close barrier", err)
					}
				case <-time.After(105 * time.Second):
					t.Fatal("relay interrupted worker did not join")
				}
				for _, network := range []string{"tcp", "udp"} {
					if err := request(network, "relay-down-"+network); err == nil {
						t.Fatal("relay outage bypassed", network)
					}
				}
				relay = startAnyTLSCoreProcess(t, relayConfig)
				for _, network := range []string{"tcp", "udp"} {
					if err := request(network, "relay-restored-"+network); err != nil {
						t.Fatalf("first new %s ID after relay restart: %v", network, err)
					}
				}
			}
			heldResult := make(chan error, 1)
			requestWorkers.Add(1)
			go func() {
				defer requestWorkers.Done()
				heldResult <- request("tcp", "held")
			}()
			select {
			case <-backend.held:
			case <-time.After(5 * time.Second):
				t.Fatal("held receipt not observed")
			}
			remote.stop(t)
			select {
			case err := <-heldResult:
				if err == nil {
					t.Fatal("held request unexpectedly succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("interrupted request worker not joined")
			}
			for _, network := range []string{"tcp", "udp"} {
				if err := request(network, "down-"+network); err == nil {
					t.Fatal("remote outage bypassed", network)
				}
			}
			restartedRemote := startAnyTLSCoreProcess(t, remoteConfig)
			for _, network := range []string{"tcp", "udp"} {
				if err := request(network, "after-"+network); err != nil {
					t.Fatalf("first new %s ID after restart: %v", network, err)
				}
			}
			// Stop producers before joining receipt workers; never judge replay
			// from a transient snapshot while the chain is still alive.
			entry.stop(t)
			if relay != nil {
				relay.stop(t)
			}
			restartedRemote.stop(t)
			backend.stop(t)
			backend.mu.Lock()
			defer backend.mu.Unlock()
			ids := []string{"before-tcp", "before-udp", "held", "after-tcp", "after-udp"}
			if relay != nil {
				ids = append(ids, "held:relay", "relay-restored-tcp", "relay-restored-udp")
			}
			if len(backend.counts) != len(ids) {
				t.Errorf("backend received %d distinct requests, want exactly %d", len(backend.counts), len(ids))
			}
			for _, id := range ids {
				if backend.counts[string(payload(id))] != 1 {
					t.Errorf("receipt count %s=%d", id, backend.counts[string(payload(id))])
				}
			}
			for _, id := range []string{"down-tcp", "down-udp", "relay-down-tcp", "relay-down-udp"} {
				if backend.counts[string(payload(id))] != 0 {
					t.Errorf("outage ID reached backend: %s", id)
				}
			}
		})
	}
}
