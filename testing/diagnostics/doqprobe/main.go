// doqprobe is an explicit, outbound-only diagnostic independent of Core DNS.
package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/apernet/quic-go"
	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
)

func main() {
	address := flag.String("address", "", "required target host:port (IP avoids resolver ambiguity)")
	serverName := flag.String("server-name", "", "required certificate name and SNI")
	mode := flag.String("mode", "native", "native (DialAddr), packet (IPv4 socket), or socks")
	proxy := flag.String("proxy", "", "explicit socks5://host:port, required for socks mode")
	bind := flag.String("bind", "0.0.0.0:0", "local IPv4 socket for packet mode")
	timeout := flag.Duration("timeout", 10*time.Second, "whole-operation deadline")
	flag.Parse()
	started := time.Now()
	stage := "configuration"
	err := func() error {
		if *address == "" || *serverName == "" || *timeout <= 0 {
			return fmt.Errorf("address, server-name and positive timeout are required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		tc := &tls.Config{ServerName: *serverName, MinVersion: tls.VersionTLS13, NextProtos: []string{"doq"}}
		qc := &quic.Config{HandshakeIdleTimeout: *timeout}
		var conn *quic.Conn
		var err error
		stage = "handshake"
		if *mode == "native" {
			conn, err = quic.DialAddr(ctx, *address, tc, qc)
		} else {
			stage = "packet-socket"
			var packet net.PacketConn
			switch *mode {
			case "packet":
				packet, err = (&net.ListenConfig{}).ListenPacket(ctx, "udp4", *bind)
			case "socks":
				if *proxy == "" {
					return fmt.Errorf("socks mode requires an explicit proxy")
				}
				client, e := socks.NewClientFromURL(N.SystemDialer, *proxy)
				if e != nil {
					return e
				}
				packet, err = client.ListenPacket(ctx, M.ParseSocksaddr(*address))
			default:
				return fmt.Errorf("unknown mode %q", *mode)
			}
			if err != nil {
				return err
			}
			defer packet.Close()
			remote, e := net.ResolveUDPAddr("udp", *address)
			if e != nil {
				return e
			}
			stage = "handshake"
			conn, err = quic.Dial(ctx, packet, remote, tc, qc)
		}
		if err != nil {
			return err
		}
		defer conn.CloseWithError(0, "diagnostic complete")
		if conn.ConnectionState().TLS.NegotiatedProtocol != "doq" {
			return fmt.Errorf("wrong ALPN")
		}
		stage = "dns-exchange"
		stream, err := conn.OpenStreamSync(ctx)
		if err != nil {
			return err
		}
		defer stream.CancelRead(0)
		defer stream.CancelWrite(0)
		deadline, _ := ctx.Deadline()
		if err := stream.SetDeadline(deadline); err != nil {
			return err
		}
		query := new(dns.Msg).SetQuestion("cloudflare.com.", dns.TypeA)
		query.Id = 0
		wire, err := query.Pack()
		if err != nil {
			return err
		}
		frame := make([]byte, 2+len(wire))
		binary.BigEndian.PutUint16(frame, uint16(len(wire)))
		copy(frame[2:], wire)
		if _, err := stream.Write(frame); err != nil {
			return err
		}
		if err := stream.Close(); err != nil {
			return err
		}
		var length uint16
		if err := binary.Read(stream, binary.BigEndian, &length); err != nil {
			return err
		}
		wire = make([]byte, length)
		if _, err := io.ReadFull(stream, wire); err != nil {
			return err
		}
		var answer dns.Msg
		if err := answer.Unpack(wire); err != nil {
			return err
		}
		if !answer.Response || answer.Id != 0 || answer.Rcode != dns.RcodeSuccess || len(answer.Answer) == 0 {
			return fmt.Errorf("invalid/empty DNS answer: %s", answer.String())
		}
		stage = "complete"
		return nil
	}()
	result := map[string]any{"mode": *mode, "address": *address, "server_name": *serverName, "stage": stage, "elapsed": time.Since(started).String(), "ok": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if err != nil {
		os.Exit(1)
	}
}
