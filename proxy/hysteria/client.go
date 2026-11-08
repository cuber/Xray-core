package hysteria

import (
	"context"
	go_errors "errors"
	"io"
	"math/rand"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/hysteria"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type Client struct {
	server        *protocol.ServerSpec
	policyManager policy.Manager

	// A relay outbound (spec024 R1) has no server of its own: it dials its session owner's connections.
	relay    *Relay
	instance *core.Instance
}

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	v := core.MustFromContext(ctx)
	p := v.GetFeature(policy.ManagerType()).(policy.Manager)

	if config.Relay != nil {
		return &Client{policyManager: p, relay: config.Relay, instance: v}, nil
	}

	streamSettings := session.StreamSettingsFromContext(ctx).(*internet.MemoryStreamConfig)
	if _, ok := streamSettings.ProtocolSettings.(*hysteria.Config); !ok {
		return nil, errors.New("not hysteria transport")
	}
	if config.Server == nil {
		return nil, errors.New(`no target server found`)
	}
	server, err := protocol.NewServerSpecFromPB(config.Server)
	if err != nil {
		return nil, errors.New("failed to get server spec").Base(err)
	}

	return &Client{
		server:        server,
		policyManager: p,
	}, nil
}

func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := session.OutboundsFromContext(ctx)
	ob := outbounds[len(outbounds)-1]
	if !ob.Target.IsValid() {
		return errors.New("target not specified")
	}
	ob.Name = "hysteria"
	ob.CanSpliceCopy = 3
	target := ob.Target

	dial := func(ctx context.Context) (stat.Connection, net.Destination, error) {
		conn, err := dialer.Dial(ctx, c.server.Destination)
		return conn, c.server.Destination, err
	}
	if c.relay != nil {
		dial = func(ctx context.Context) (stat.Connection, net.Destination, error) {
			conn, server, err := c.dialRelay(ctx)
			if err != nil {
				return nil, server, err
			}
			// The stream runs on the owner's connections, not through this outbound's Dial, so it is counted under
			// this outbound's tag here.
			if counter, ok := dialer.(connectionCounter); ok {
				conn = counter.CountConnection(conn)
			}
			return conn, server, nil
		}
	}
	conn, server, err := dial(hysteria.ContextWithDatagram(ctx, target.Network == net.Network_UDP))
	if err != nil {
		return errors.New("failed to find an available destination").Base(err)
	}
	defer conn.Close()
	errors.LogInfo(ctx, "tunneling request to ", target, " via ", target.Network, ":", server.NetAddr())

	var newCtx context.Context
	var newCancel context.CancelFunc
	if session.TimeoutOnlyFromContext(ctx) {
		newCtx, newCancel = context.WithCancel(context.Background())
	}

	sessionPolicy := c.policyManager.ForLevel(0)
	ctx, cancel := context.WithCancel(ctx)
	timer := signal.CancelAfterInactivity(ctx, func() {
		cancel()
		if newCancel != nil {
			newCancel()
		}
	}, sessionPolicy.Timeouts.ConnectionIdle)

	if newCtx != nil {
		ctx = newCtx
	}

	if target.Network == net.Network_TCP {
		requestDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)
			bufferedWriter := buf.NewBufferedWriter(buf.NewWriter(conn))
			err := WriteTCPRequest(bufferedWriter, target.NetAddr(), sendsPadding(conn))
			if err != nil {
				return errors.New("failed to write request").Base(err)
			}
			if err := bufferedWriter.SetBuffered(false); err != nil {
				return err
			}
			return buf.Copy(link.Reader, bufferedWriter, buf.UpdateActivity(timer))
		}

		responseDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)
			ok, msg, err := ReadTCPResponse(conn)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New(msg)
			}
			return buf.Copy(buf.NewReader(conn), link.Writer, buf.UpdateActivity(timer))
		}

		responseDoneAndCloseWriter := task.OnSuccess(responseDone, task.Close(link.Writer))
		if err := task.Run(ctx, requestDone, responseDoneAndCloseWriter); err != nil {
			return errors.New("connection ends").Base(err)
		}

		return nil
	}

	if target.Network == net.Network_UDP {
		iConn := stat.TryUnwrapStatsConn(conn)
		_, ok := iConn.(*hysteria.InterConn)
		if !ok {
			return errors.New("udp requires hysteria udp transport")
		}

		requestDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)

			writer := &UDPWriter{
				writer: conn,
				addr:   target.NetAddr(),
			}

			if err := buf.Copy(link.Reader, writer, buf.UpdateActivity(timer)); err != nil {
				return errors.New("failed to transport all UDP request").Base(err)
			}

			return nil
		}

		responseDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)

			reader := &UDPReader{
				reader: conn,
				df:     &Defragger{},
			}

			if err := buf.Copy(reader, link.Writer, buf.UpdateActivity(timer)); err != nil {
				return errors.New("failed to transport all UDP response").Base(err)
			}

			return nil
		}

		responseDoneAndCloseWriter := task.OnSuccess(responseDone, task.Close(link.Writer))
		if err := task.Run(ctx, requestDone, responseDoneAndCloseWriter); err != nil {
			return errors.New("connection ends").Base(err)
		}

		return nil
	}

	return nil
}

// sessionOwner resolves a relay outbound's owner: its server and stream settings (spec024 R1).
func (c *Client) sessionOwner() (*Client, *internet.MemoryStreamConfig, error) {
	ohm, _ := c.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	if ohm == nil {
		return nil, nil, errors.New("hysteria relay: no outbound manager")
	}
	handler, ok := ohm.GetHandler(c.relay.Session).(interface {
		GetOutbound() proxy.Outbound
		StreamSettings() *internet.MemoryStreamConfig
	})
	if !ok {
		return nil, nil, errors.New("hysteria relay: no session owner ", c.relay.Session)
	}
	owner, ok := handler.GetOutbound().(*Client)
	if !ok || owner.server == nil {
		return nil, nil, errors.New("hysteria relay: ", c.relay.Session, " is not a Hy2 session owner")
	}
	return owner, handler.StreamSettings(), nil
}

// connectionCounter is the outbound handler's traffic accounting, which its Dial applies to what it dials itself.
type connectionCounter interface {
	CountConnection(stat.Connection) stat.Connection
}

// dialRelay opens the stream or UDP session on the owner's connections as the relayed user (spec024 R2).
func (c *Client) dialRelay(ctx context.Context) (stat.Connection, net.Destination, error) {
	owner, streamSettings, err := c.sessionOwner()
	if err != nil {
		return nil, net.Destination{}, err
	}
	conn, err := hysteria.Dial(hysteria.ContextWithRelayIndex(ctx, c.relay.Index), owner.server.Destination, streamSettings)
	return conn, owner.server.Destination, err
}

// RelayStatus reports a relay outbound's binding without dialing (spec024 R6, R7): the status and, once
// bound, the user's email. ok is false for an outbound that is not a relay.
func (c *Client) RelayStatus() (status, user string, ok bool) {
	if c.relay == nil {
		return "", "", false
	}
	owner, streamSettings, err := c.sessionOwner()
	if err != nil {
		return hysteria.RelayNoConnection, "", true
	}
	status, user = hysteria.RelayStatus(c.instance, owner.server.Destination, streamSettings, c.relay.Index)
	return status, user, true
}

// RelaySession is the relay outbound's owner tag, empty for other outbounds.
func (c *Client) RelaySession() string {
	return c.relay.GetSession()
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}

type UDPWriter struct {
	writer io.Writer
	addr   string
	buf    [buf.Size]byte
}

func (w *UDPWriter) SendMessage(msg *UDPMessage) error {
	msgN := msg.Serialize(w.buf[:])
	if msgN < 0 {
		return nil
	}
	_, err := w.writer.Write(w.buf[:msgN])
	return err
}

func (w *UDPWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for i, b := range mb {
		addr := w.addr
		if b.UDP != nil {
			addr = b.UDP.NetAddr()
		}

		msg := &UDPMessage{
			SessionID: 0,
			PacketID:  0,
			FragID:    0,
			FragCount: 1,
			Addr:      addr,
			Data:      b.Bytes(),
		}

		err := w.SendMessage(msg)
		var errTooLarge *quic.DatagramTooLargeError
		if go_errors.As(err, &errTooLarge) {
			msg.PacketID = uint16(rand.Intn(0xFFFF)) + 1
			fMsgs := FragUDPMessage(msg, int(errTooLarge.MaxDatagramPayloadSize))
			for _, fMsg := range fMsgs {
				err := w.SendMessage(&fMsg)
				if err != nil {
					buf.ReleaseMulti(mb[i:])
					return err
				}
			}
		} else if err != nil {
			buf.ReleaseMulti(mb[i:])
			return err
		}

		b.Release()
	}

	return nil
}

type UDPReader struct {
	reader   io.Reader
	df       *Defragger
	firstBuf *buf.Buffer
}

func (r *UDPReader) ReadFrom(p []byte) (n int, addr *net.Destination, err error) {
	for {
		var packet [1500]byte

		n, err := r.reader.Read(packet[:])
		if err != nil {
			return 0, nil, err
		}

		msg, err := ParseUDPMessage(packet[:n])
		if err != nil {
			continue
		}

		dfMsg := r.df.Feed(msg)
		if dfMsg == nil {
			continue
		}

		dest, err := net.ParseDestination("udp:" + dfMsg.Addr)
		if err != nil {
			continue
		}

		if len(p) < len(dfMsg.Data) {
			continue
		}

		return copy(p, dfMsg.Data), &dest, nil
	}
}

func (r *UDPReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.firstBuf != nil {
		mb := buf.MultiBuffer{r.firstBuf}
		r.firstBuf = nil
		return mb, nil
	}
	b := buf.New()
	b.Resize(0, buf.Size)
	n, addr, err := r.ReadFrom(b.Bytes())
	if err != nil {
		b.Release()
		return nil, err
	}
	b.Resize(0, int32(n))
	b.UDP = addr
	return buf.MultiBuffer{b}, nil
}
