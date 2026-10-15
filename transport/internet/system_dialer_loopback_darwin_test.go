//go:build darwin

package internet

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
)

func TestSystemDialerLoopbackUDPWire(t *testing.T) {
	ControllersLock.Lock()
	previous := Controllers
	Controllers = append(append([]func(string, string, syscall.RawConn) error(nil), previous...), func(_, address string, raw syscall.RawConn) error {
		if !strings.HasPrefix(address, "0.0.0.0:") && !strings.HasPrefix(address, "[::]:") {
			return nil
		}
		var setErr error
		if err := raw.Control(func(fd uintptr) {
			setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		}); err != nil {
			return err
		}
		return setErr
	})
	ControllersLock.Unlock()
	defer func() { ControllersLock.Lock(); Controllers = previous; ControllersLock.Unlock() }()
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	port := server.LocalAddr().(*net.UDPAddr).Port
	conn, err := (&DefaultSystemDialer{}).Dial(context.Background(), nil, xnet.UDPDestination(xnet.LocalHostIP, xnet.Port(port)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	local := conn.LocalAddr().(*net.UDPAddr)
	// SO_REUSEPORT fixes the collision condition deterministically only when
	// the real dialer selected wildcard. Concrete clients never receive it.
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var setErr error
		if err := raw.Control(func(fd uintptr) { setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1) }); err != nil {
			return err
		}
		return setErr
	}}
	victim, bindErr := lc.ListenPacket(context.Background(), "udp4", (&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: local.Port}).String())
	if victim != nil {
		defer victim.Close()
	} else if !errors.Is(bindErr, syscall.EADDRINUSE) {
		t.Fatalf("competing IPv4 bind: %v", bindErr)
	}
	request := []byte("system-udp-loopback-exact-payload")
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	server.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 128)
	n, peer, err := server.ReadFromUDP(buffer)
	if err != nil || !bytes.Equal(buffer[:n], request) {
		t.Fatalf("server receipt n=%d peer=%v err=%v", n, peer, err)
	}
	if _, err := server.WriteToUDP(buffer[:n], peer); err != nil {
		t.Fatal(err)
	}
	if victim != nil {
		victim.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := victim.ReadFrom(buffer)
		t.Fatalf("reply stolen by competing IPv4 socket: client=%s competing=%s received=%d from=%v exact=%t err=%v", local, victim.LocalAddr(), n, from, bytes.Equal(buffer[:n], request), err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err = conn.Read(buffer)
	if err != nil || !bytes.Equal(buffer[:n], request) {
		t.Fatalf("client exact reply n=%d err=%v", n, err)
	}
	if !local.IP.Equal(net.IPv4(127, 0, 0, 1)) || local.Port == port {
		t.Fatalf("unreserved source tuple: local=%s server=%s", local, server.LocalAddr())
	}
}
