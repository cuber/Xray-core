package anytls

import (
	"bytes"
	"net"
	"testing"
	"time"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/singbridge"
)

func TestClientUDPPacketBoundaries(t *testing.T) {
	for _, address := range []string{"127.0.0.1:53", "[::1]:53", "udp.test:53"} {
		for _, size := range []int{0, 1, 8192, 8193} {
			a, b := net.Pipe()
			a.SetDeadline(time.Now().Add(time.Second))
			b.SetDeadline(time.Now().Add(time.Second))
			dest := M.ParseSocksaddr(address)
			packets := &clientPackets{conn: uot.NewConn(a, uot.Request{}), target: singbridge.ToDestination(dest, xnet.Network_UDP)}
			peer := uot.NewConn(b, uot.Request{})
			payload := bytes.Repeat([]byte{42}, size)
			buffer := buf.NewWithSize(int32(size + 1))
			buffer.Write(payload)
			done := make(chan error, 1)
			go func() { done <- packets.WriteMultiBuffer(buf.MultiBuffer{buffer}) }()
			valid := size > 0 && size <= 8192
			if valid {
				read := B.NewSize(65535)
				gotDest, err := peer.ReadPacket(read)
				if err != nil || gotDest != dest || !bytes.Equal(read.Bytes(), payload) {
					t.Fatalf("upload %s/%d: %v %v", address, size, gotDest, err)
				}
				read.Release()
			}
			if err := <-done; (err == nil) != valid {
				t.Fatalf("size %d accepted=%v", size, err == nil)
			}
			go func() { done <- peer.WritePacket(B.As(payload), dest) }()
			mb, err := packets.ReadMultiBuffer()
			if (err == nil) != valid {
				t.Fatalf("download size %d: %v", size, err)
			}
			if valid && (len(mb) != 1 || mb[0].UDP == nil || *mb[0].UDP != packets.target || !bytes.Equal(mb[0].Bytes(), payload)) {
				t.Fatalf("download boundary lost for %s/%d", address, size)
			}
			buf.ReleaseMulti(mb)
			<-done
			a.Close()
			b.Close()
		}
	}
}
