package anytls

import (
	"context"
	"fmt"
	"sync"
	"time"

	B "github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/transport"
)

type udpTarget struct {
	ctx   context.Context
	link  *transport.Link
	timer *signal.ActivityTimer
}

func (s *Server) acquireUDPLink(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil || s.activeUDPLinks >= maxUDPLinks {
		return false
	}
	s.activeUDPLinks++
	return true
}

func (s *Server) releaseUDPLink() {
	s.mu.Lock()
	s.activeUDPLinks--
	s.mu.Unlock()
}

// Each destination gets its own dispatcher link: a UoT association is not a route.
func (s *Server) serveUDP(parent context.Context, conn net.Conn) error {
	user := *session.InboundFromContext(parent).User
	p := s.policy.ForLevel(user.Level)
	conn.SetReadDeadline(time.Now().Add(p.Timeouts.Handshake))
	request, err := uot.ReadRequest(conn)
	if err != nil {
		return err
	}
	conn.SetReadDeadline(time.Time{})
	packets := uot.NewConn(conn, *request)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	idle := signal.CancelAfterInactivity(ctx, cancel, p.Timeouts.ConnectionIdle)
	defer idle.SetTimeout(0)
	targets := map[net.Destination]*udpTarget{}
	var workers sync.WaitGroup
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
		dest, e := packets.ReadPacket(input)
		if e != nil {
			return e
		}
		idle.Update()
		// Xray's UDP reader uses buf.Size datagrams. Reject larger input rather
		// than accepting a packet whose response the existing stack can truncate.
		if !dest.IsValid() || dest.Port == 0 || input.Len() == 0 || input.Len() > buf.Size {
			return fmt.Errorf("anytls: invalid UDP target or unsupported payload size")
		}
		for address, existing := range targets {
			if existing.ctx.Err() != nil {
				existing.timer.SetTimeout(0)
				delete(targets, address)
			}
		}
		target := singbridge.ToDestination(dest, net.Network_UDP)
		t := targets[target]
		if t == nil {
			if len(targets) >= 64 {
				return fmt.Errorf("anytls: UDP target limit")
			}
			child, childCancel := context.WithCancel(streamContext(ctx, user, conn))
			if !s.acquireUDPLink(child) {
				childCancel()
				return fmt.Errorf("anytls: shared UDP target limit")
			}
			link, e := session.DispatcherFromContext(child).Dispatch(child, target)
			if e != nil {
				childCancel()
				s.releaseUDPLink()
				return e
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
				// Map eviction does not release admission: wait for this worker,
				// then interrupt both pipes before making its slot reusable.
				defer s.releaseUDPLink()
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
						from := target
						if b.UDP != nil {
							from = *b.UDP
						}
						payload := B.As(b.Bytes())
						writeErr := packets.WritePacket(payload, singbridge.ToSocksaddr(from))
						// WritePacket's vectorised path owns its buffers; As wraps Xray-owned data.
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
		if e := t.link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); e != nil {
			return e
		}
	}
}
