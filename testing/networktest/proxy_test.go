package networktest

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/protocol/socks/socks5"
)

func TestProxyRouting(t *testing.T) {
	for _, udp := range []bool{false, true} {
		t.Run(map[bool]string{false: "tcp", true: "udp-rejected"}[udp], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				if _, err = socks5.ReadAuthRequest(reader); err == nil {
					err = socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeNotRequired})
				}
				if err == nil {
					var request socks5.Request
					request, err = socks5.ReadRequest(reader)
					if err == nil {
						code := byte(socks5.ReplyCodeSuccess)
						if request.Command == socks5.CommandUDPAssociate {
							code = socks5.ReplyCodeUnsupported
						}
						err = socks5.WriteResponse(conn, socks5.Response{ReplyCode: code, Bind: M.ParseSocksaddr("127.0.0.1:1")})
						if err == nil && !udp {
							_, err = io.WriteString(conn, request.Destination.String())
						}
					}
				}
				done <- err
			}()
			t.Setenv("XRAY_TEST_NETWORK", "1")
			t.Setenv("XRAY_TEST_PROXY", "")
			t.Setenv("XRAY_TEST_UDP_PROXY", "")
			key := "XRAY_TEST_PROXY"
			if udp {
				key = "XRAY_TEST_UDP_PROXY"
			}
			t.Setenv(key, "socks5://"+listener.Addr().String())
			p := Enable(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if udp {
				conn, err := p.ListenPacket(ctx, "192.0.2.1:853")
				if err == nil {
					conn.Close()
					t.Error("rejected UDP must not fall back to direct")
				}
			} else {
				conn, err := p.DialContext(ctx, "tcp", "fixture.invalid:443")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				data, err := io.ReadAll(conn)
				if err != nil || string(data) != "fixture.invalid:443" {
					t.Fatalf("SOCKS domain forwarding: %q, %v", data, err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
