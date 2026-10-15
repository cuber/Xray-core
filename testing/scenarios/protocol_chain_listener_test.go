package scenarios

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

type protocolChainListenerKey struct {
	network, address string
}

type protocolChainListenerOwner struct {
	mu       sync.Mutex
	expected []protocolChainListenerKey
	captured map[protocolChainListenerKey][]syscall.RawConn
	issues   []string
}

var protocolChainListenerObservers = struct {
	sync.Mutex
	owners map[string]*protocolChainListenerOwner
}{owners: make(map[string]*protocolChainListenerOwner)}

func init() {
	// Register before tests start: the production registry has no mutation lock.
	if err := internet.RegisterListenerController(func(network, address string, raw syscall.RawConn) error {
		protocolChainListenerObservers.Lock()
		owner := protocolChainListenerObservers.owners[address]
		protocolChainListenerObservers.Unlock()
		if owner != nil {
			key := protocolChainListenerKey{network, address}
			owner.mu.Lock()
			if _, ok := owner.captured[key]; !ok {
				owner.issues = append(owner.issues, fmt.Sprintf("unexpected physical listener %s %s", network, address))
			} else {
				owner.captured[key] = append(owner.captured[key], raw)
			}
			owner.mu.Unlock()
		}
		return nil
	}); err != nil {
		panic(err)
	}
}

func protocolChainExpectedListeners(config map[string]any) ([]protocolChainListenerKey, error) {
	var expected []protocolChainListenerKey
	ins, ok := config["inbounds"].([]any)
	if !ok && config["inbounds"] != nil {
		return nil, errors.New("invalid fixture inbound list")
	}
	for _, value := range ins {
		in, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid fixture inbound")
		}
		port, ok := in["port"].(int)
		if !ok || port <= 0 || port > 65535 || in["listen"] != "127.0.0.1" {
			return nil, errors.New("fixture listener must have an explicit IPv4 loopback port")
		}
		stream, _ := in["streamSettings"].(map[string]any)
		physical := "raw"
		if stream != nil {
			physical, _ = stream["network"].(string)
		}
		var networks []string
		switch in["protocol"] {
		case "hysteria":
			if physical != "hysteria" {
				return nil, errors.New("fixture Hy2 listener must use the hysteria transport")
			}
			networks = []string{"udp4"}
		case "vless", "anytls":
			if physical != "raw" {
				return nil, fmt.Errorf("unsupported fixture stream %q", physical)
			}
			networks = []string{"tcp4"}
		case "shadowsocks", "dokodemo-door":
			if physical != "raw" {
				return nil, fmt.Errorf("unsupported fixture stream %q", physical)
			}
			settings, _ := in["settings"].(map[string]any)
			network, _ := settings["network"].(string)
			for _, item := range strings.Split(network, ",") {
				if item != "tcp" && item != "udp" {
					return nil, fmt.Errorf("unsupported fixture inbound network %q", item)
				}
				networks = append(networks, item+"4")
			}
		default:
			return nil, fmt.Errorf("unobserved fixture inbound protocol %v", in["protocol"])
		}
		for _, network := range networks {
			expected = append(expected, protocolChainListenerKey{network, fmt.Sprintf("127.0.0.1:%d", port)})
		}
	}
	return expected, nil
}

func protocolChainWatchListeners(t *testing.T, expected []protocolChainListenerKey) *protocolChainListenerOwner {
	t.Helper()
	owner := &protocolChainListenerOwner{expected: expected, captured: make(map[protocolChainListenerKey][]syscall.RawConn)}
	protocolChainListenerObservers.Lock()
	defer protocolChainListenerObservers.Unlock()
	for _, key := range expected {
		if prior := protocolChainListenerObservers.owners[key.address]; prior != nil && prior != owner {
			t.Fatalf("concurrent fixture listener owner: %s", key.address)
		}
		if _, duplicate := owner.captured[key]; duplicate {
			t.Fatalf("duplicate fixture listener expectation: %v", key)
		}
		owner.captured[key] = nil
	}
	for _, key := range expected {
		protocolChainListenerObservers.owners[key.address] = owner
	}
	return owner
}

func TestProtocolChainListenerMissingCapture(t *testing.T) {
	owner := &protocolChainListenerOwner{
		expected: []protocolChainListenerKey{{"udp4", "127.0.0.1:40000"}},
		captured: make(map[protocolChainListenerKey][]syscall.RawConn),
	}
	for _, closed := range []bool{false, true} {
		if err := owner.check(closed); err == nil {
			t.Fatalf("zero captures incorrectly accepted: closed=%t", closed)
		}
	}
	if _, err := protocolChainExpectedListeners(map[string]any{"inbounds": []map[string]any{{"protocol": "vless"}}}); err == nil {
		t.Fatal("unrecognized inbound list silently removed listener expectations")
	}
}

func (o *protocolChainListenerOwner) release() {
	protocolChainListenerObservers.Lock()
	defer protocolChainListenerObservers.Unlock()
	for _, key := range o.expected {
		if protocolChainListenerObservers.owners[key.address] == o {
			delete(protocolChainListenerObservers.owners, key.address)
		}
	}
}

func (o *protocolChainListenerOwner) check(closed bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.issues) != 0 {
		return fmt.Errorf("listener capture issues: %v", o.issues)
	}
	for _, key := range o.expected {
		captures := o.captured[key]
		if len(captures) != 1 || captures[0] == nil {
			return fmt.Errorf("%s %s listener captures=%d want=1 nonnil original RawConn", key.network, key.address, len(captures))
		}
		called := false
		err := captures[0].Control(func(uintptr) { called = true })
		if closed {
			if !errors.Is(err, net.ErrClosed) || called {
				return fmt.Errorf("original %s listener %s did not retire: callback=%t err=%v", key.network, key.address, called, err)
			}
		} else if err != nil || !called {
			return fmt.Errorf("original %s listener %s is not live: callback=%t err=%v", key.network, key.address, called, err)
		}
	}
	return nil
}

func TestProtocolChainListenerOwnershipOracle(t *testing.T) {
	protocolChainNetworkWindow(t)
	for _, network := range []string{"tcp4", "udp4"} {
		t.Run(network, func(t *testing.T) {
			port := protocolChainPort(t)
			address := fmt.Sprintf("127.0.0.1:%d", port)
			owner := protocolChainWatchListeners(t, []protocolChainListenerKey{{network, address}})
			defer owner.release()
			var original interface{ Close() error }
			var err error
			if network == "tcp4" {
				original, err = internet.ListenSystem(context.Background(), &xnet.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}, nil)
			} else {
				original, err = internet.ListenSystemPacket(context.Background(), &xnet.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer original.Close()
			owner.release()
			if err := owner.check(false); err != nil {
				t.Fatal(err)
			}
			if err := owner.check(true); err == nil {
				t.Fatal("live original listener incorrectly accepted as retired")
			}
			if err := original.Close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.check(true); err != nil {
				t.Fatal(err)
			}
			var foreign interface{ Close() error }
			if network == "tcp4" {
				foreign, err = net.Listen(network, address)
			} else {
				foreign, err = net.ListenPacket(network, address)
			}
			if err != nil {
				t.Fatal("foreign same-port listener:", err)
			}
			defer foreign.Close()
			if err := owner.check(true); err != nil {
				t.Fatal("foreign listener confused original ownership:", err)
			}
			raw, err := foreign.(syscall.Conn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			called := false
			if err := raw.Control(func(uintptr) { called = true }); err != nil || !called {
				t.Fatalf("ownership oracle closed the foreign listener: callback=%t err=%v", called, err)
			}
		})
	}
}

func TestProtocolChainListenerCapture(t *testing.T) {
	protocolChainNetworkWindow(t)
	pki := protocolChainCertificate(t)
	for _, protocol := range protocolChainProtocols {
		t.Run(protocol, func(t *testing.T) {
			in, _ := protocolChainPair(t, pki, protocol, 0, protocolChainPort(t), 0, false)
			c := protocolChainStart(t, protocolChainBase([]any{in}, []any{protocolChainFreedom()}))
			want := 1
			if protocol == "ss2" {
				want = 2
			}
			if len(c.listeners.expected) != want {
				t.Fatalf("%s physical listener expectations=%d want=%d", protocol, len(c.listeners.expected), want)
			}
			c.close(t)
		})
	}
	for _, network := range []string{"tcp", "udp"} {
		t.Run("doko_"+network, func(t *testing.T) {
			in := map[string]any{
				"tag": "in", "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": protocolChainPort(t),
				"settings": map[string]any{"address": "127.0.0.1", "port": 9, "network": network},
			}
			c := protocolChainStart(t, protocolChainBase([]any{in}, []any{protocolChainFreedom()}))
			if len(c.listeners.expected) != 1 || c.listeners.expected[0].network != network+"4" {
				t.Fatalf("doko %s physical listener expectations=%v", network, c.listeners.expected)
			}
			c.close(t)
		})
	}
}
