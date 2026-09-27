package anytls

import (
	"fmt"

	B "github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/singbridge"
)

type clientPackets struct {
	conn   *uot.Conn
	target net.Destination
}

func (p *clientPackets) WriteMultiBuffer(mb buf.MultiBuffer) error {
	defer buf.ReleaseMulti(mb)
	for _, b := range mb {
		dest := p.target
		if b.UDP != nil {
			dest = *b.UDP
		}
		if b.IsEmpty() || b.Len() > buf.Size || !dest.IsValid() || dest.Port == 0 {
			return fmt.Errorf("anytls: invalid UDP target or unsupported payload size")
		}
		if err := p.conn.WritePacket(B.As(b.Bytes()), singbridge.ToSocksaddr(dest)); err != nil {
			return err
		}
	}
	return nil
}

func (p *clientPackets) ReadMultiBuffer() (buf.MultiBuffer, error) {
	// The full wire length is read before enforcing Core's UDP payload limit.
	// A short buffer must never turn an oversized datagram into valid traffic.
	packet := B.NewSize(65535)
	defer packet.Release()
	dest, err := p.conn.ReadPacket(packet)
	if err != nil {
		return nil, err
	}
	if packet.Len() == 0 || packet.Len() > buf.Size || !dest.IsValid() || dest.Port == 0 {
		return nil, fmt.Errorf("anytls: invalid UDP response or unsupported payload size")
	}
	b := buf.New()
	b.Write(packet.Bytes())
	target := singbridge.ToDestination(dest, net.Network_UDP)
	b.UDP = &target
	return buf.MultiBuffer{b}, nil
}
