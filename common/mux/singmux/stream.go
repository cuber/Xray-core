package singmux

import (
	"context"
	stderrors "errors"
	"io"
	"sync"

	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/transport"
)

const (
	// maxUDPTargets bounds the destinations of one UDP stream; each owns a link.
	maxUDPTargets = 64
	// packetHeadroom covers the status byte, length and address a packet write prepends.
	packetHeadroom = 3 + M.MaxSocksaddrLength
)

type handler struct {
	s *Server
}

func (h handler) NewConnectionEx(ctx context.Context, conn net.Conn, _, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	s := h.s
	c, reason := s.admit(ctx)
	if reason != "" {
		s.refuseStream(ctx, conn, onClose, reason)
		return
	}
	defer s.release(c)
	target := toDestination(destination, net.Network_TCP)
	if reason := targetRefusal(target); reason != "" {
		s.refuseStream(ctx, conn, onClose, reason)
		return
	}
	s.stats.openStream(net.Network_TCP)
	defer s.stats.closeStream(net.Network_TCP)
	if err := s.relayTCP(ctx, conn, target, c.level); err != nil {
		errors.LogInfoInner(ctx, err, "sing-mux stream to ", target, " ends")
	}
}

func (h handler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, _, _ M.Socksaddr, onClose N.CloseHandlerFunc) {
	s := h.s
	c, reason := s.admit(ctx)
	if reason != "" {
		s.refuseStream(ctx, conn, onClose, reason)
		return
	}
	defer s.release(c)
	s.stats.openStream(net.Network_UDP)
	defer s.stats.closeStream(net.Network_UDP)
	if err := s.relayUDP(ctx, conn, c.level); err != nil && !stderrors.Is(err, io.EOF) {
		errors.LogInfoInner(ctx, err, "sing-mux packet stream ends")
	}
}

func (s *Server) refuseStream(ctx context.Context, conn io.Closer, onClose N.CloseHandlerFunc, reason string) {
	s.stats.reject(reason)
	err := errors.New("sing-mux stream refused: ", reason)
	errors.LogInfo(ctx, err)
	N.CloseOnHandshakeFailure(conn, onClose, err)
}

// targetRefusal rejects invalid targets and nested multiplexing; streams are
// dispatched past the mux layer, so a nested carrier would only be dialed.
func targetRefusal(target net.Destination) string {
	if !target.IsValid() || target.Port == 0 {
		return rejectInvalid
	}
	if IsDestination(target) || (target.Address.Family().IsDomain() && target.Address.Domain() == "v1.mux.cool") {
		return rejectNested
	}
	return ""
}

func accessMessage(ctx context.Context, dest net.Destination) *log.AccessMessage {
	msg := &log.AccessMessage{To: dest, Status: log.AccessAccepted}
	if inbound := session.InboundFromContext(ctx); inbound != nil {
		if inbound.Source.IsValid() {
			msg.From = inbound.Source
		}
		if inbound.User != nil {
			msg.Email = inbound.User.Email
		}
	}
	return msg
}

func (s *Server) relayTCP(ctx context.Context, conn net.Conn, dest net.Destination, level uint32) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = log.ContextWithAccessMessage(ctx, accessMessage(ctx, dest))
	link, err := s.dispatcher.Dispatch(ctx, dest)
	if err != nil {
		s.stats.reject(rejectDispatch)
		N.ReportHandshakeFailure(conn, err)
		conn.Close()
		return err
	}
	cleanup := func() {
		cancel()
		conn.Close()
		common.Interrupt(link.Reader)
		common.Interrupt(link.Writer)
	}
	stop := context.AfterFunc(ctx, cleanup)
	defer stop()
	defer cleanup()
	p := s.policy.ForLevel(level)
	timer := signal.CancelAfterInactivity(ctx, cancel, p.Timeouts.ConnectionIdle)
	defer timer.SetTimeout(0)
	done := make(chan error, 2)
	go func() {
		e := buf.Copy(buf.NewReader(conn), link.Writer, buf.UpdateActivity(timer))
		common.Close(link.Writer)
		timer.SetTimeout(p.Timeouts.DownlinkOnly)
		done <- e
	}()
	go func() {
		e := buf.Copy(link.Reader, buf.NewWriter(conn), buf.UpdateActivity(timer))
		timer.SetTimeout(p.Timeouts.UplinkOnly)
		done <- e
	}()
	err = <-done
	if err != nil {
		cleanup()
	}
	other := <-done
	if err == nil {
		err = other
	}
	if err == io.EOF {
		return nil
	}
	return err
}

type udpTarget struct {
	ctx   context.Context
	link  *transport.Link
	timer *signal.ActivityTimer
}

// relayUDP gives each destination of a packet stream its own dispatcher link.
// Fixed-destination streams always read one destination; PacketAddr streams
// carry the destination with every packet.
func (s *Server) relayUDP(parent context.Context, conn N.PacketConn, level uint32) error {
	p := s.policy.ForLevel(level)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	idle := signal.CancelAfterInactivity(ctx, cancel, p.Timeouts.ConnectionIdle)
	defer idle.SetTimeout(0)
	targets := map[net.Destination]*udpTarget{}
	var workers sync.WaitGroup
	var writeMu sync.Mutex
	defer func() {
		cancel()
		for _, t := range targets {
			t.timer.SetTimeout(0)
		}
		workers.Wait()
	}()
	input := B.NewSize(65535)
	defer input.Release()
	for {
		input.Reset()
		dest, err := conn.ReadPacket(input)
		if err != nil {
			return err
		}
		idle.Update()
		// Xray's UDP reader uses buf.Size datagrams; a larger one could be truncated.
		if !dest.IsValid() || dest.Port == 0 || input.Len() == 0 || input.Len() > buf.Size {
			return errors.New("sing-mux: invalid UDP target or unsupported payload size")
		}
		for address, existing := range targets {
			if existing.ctx.Err() != nil {
				existing.timer.SetTimeout(0)
				delete(targets, address)
			}
		}
		target := toDestination(dest, net.Network_UDP)
		t := targets[target]
		if t == nil {
			if len(targets) >= maxUDPTargets {
				return errors.New("sing-mux: UDP target limit")
			}
			child, childCancel := context.WithCancel(streamContext(ctx))
			child = log.ContextWithAccessMessage(child, accessMessage(child, target))
			link, err := s.dispatcher.Dispatch(child, target)
			if err != nil {
				childCancel()
				s.stats.reject(rejectDispatch)
				return err
			}
			closeLink := func() {
				childCancel()
				common.Interrupt(link.Reader)
				common.Interrupt(link.Writer)
			}
			t = &udpTarget{ctx: child, link: link}
			t.timer = signal.CancelAfterInactivity(child, closeLink, p.Timeouts.ConnectionIdle)
			targets[target] = t
			stopLink := context.AfterFunc(child, closeLink)
			workers.Add(1)
			go func(t *udpTarget, target net.Destination) {
				defer workers.Done()
				defer closeLink()
				defer stopLink()
				defer t.timer.SetTimeout(0)
				for {
					mb, e := t.link.Reader.ReadMultiBuffer()
					if len(mb) > 0 {
						idle.Update()
						t.timer.Update()
					}
					for _, b := range mb {
						if b == nil {
							continue
						}
						from := target
						if b.UDP != nil {
							from = *b.UDP
						}
						packet := B.NewSize(packetHeadroom + int(b.Len()))
						packet.Resize(packetHeadroom, 0)
						common.Must2(packet.Write(b.Bytes()))
						writeMu.Lock()
						// WritePacket owns and releases the packet buffer.
						writeErr := conn.WritePacket(packet, toSocksaddr(from))
						writeMu.Unlock()
						if writeErr != nil {
							buf.ReleaseMulti(mb)
							cancel()
							return
						}
					}
					buf.ReleaseMulti(mb)
					if e != nil {
						return
					}
				}
			}(t, target)
		}
		t.timer.Update()
		b := buf.NewWithSize(int32(input.Len()))
		b.Write(input.Bytes())
		b.UDP = &target
		if err := t.link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
			return err
		}
	}
}
