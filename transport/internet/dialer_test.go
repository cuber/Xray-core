package internet_test

import (
	"context"
	"fmt"
	"io"
	stdnet "net"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/testing/servers/tcp"
	. "github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
)

func TestDialWithLocalAddr(t *testing.T) {
	server := &tcp.Server{}
	dest, err := server.Start()
	common.Must(err)
	defer server.Close()

	conn, err := DialSystem(context.Background(), net.TCPDestination(net.LocalHostIP, dest.Port), nil)
	common.Must(err)
	if r := cmp.Diff(conn.RemoteAddr().String(), "127.0.0.1:"+dest.Port.String()); r != "" {
		t.Error(r)
	}
	conn.Close()
}

func TestDialUnixDestination(t *testing.T) {
	socketPath := t.TempDir() + "/probe.sock"
	listener, err := stdnet.Listen("unix", socketPath)
	common.Must(err)
	defer listener.Close()
	defer os.Remove(socketPath)

	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			done <- err
			return
		}
		if string(buf) != "ping" {
			done <- fmt.Errorf("unexpected request: %s", cmp.Diff("ping", string(buf)))
			return
		}
		_, err = conn.Write([]byte("pong"))
		done <- err
	}()

	conn, err := Dial(context.Background(), net.UnixDestination(net.DomainAddress(socketPath)), nil)
	common.Must(err)
	defer conn.Close()

	_, err = conn.Write([]byte("ping"))
	common.Must(err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	common.Must(err)
	if diff := cmp.Diff("pong", string(buf)); diff != "" {
		t.Fatal(diff)
	}
	common.Must(<-done)
}
