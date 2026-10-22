// Package singmux serves sing-mux sessions, the multiplexing that sing-box and
// mihomo clients open by dialing Destination through a proxy. Every stream is
// dispatched like a direct connection of the carrier's user on the same inbound.
package singmux

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sm "github.com/sagernet/sing-mux"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common"
	xctx "github.com/xtls/xray-core/common/ctx"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
)

const (
	// Domain and Port form the carrier destination of sing-box and mihomo clients.
	Domain = "sp.mux.sing-box.arpa"
	Port   = 444
	// DefaultMaxStreams bounds the concurrent streams of one carrier.
	DefaultMaxStreams = 256
	// MaxStreamsLimit is the largest configurable per-carrier bound.
	MaxStreamsLimit = 4096
)

// IsDestination reports whether dest is the sing-mux carrier destination.
func IsDestination(dest net.Destination) bool {
	return dest.Port == Port && dest.Address != nil && dest.Address.Family().IsDomain() &&
		strings.EqualFold(dest.Address.Domain(), Domain)
}

// Options configures one inbound's sing-mux server.
type Options struct {
	Enabled    bool
	MaxStreams uint32
	// UserActive reports whether the carrier's user still belongs to the inbound.
	// Nil admits every stream.
	UserActive func(ctx context.Context, email string) bool
}

// Server accepts the sing-mux carriers of one inbound.
type Server struct {
	dispatcher routing.Dispatcher
	policy     policy.Manager
	options    Options
	service    *sm.Service
	stats      counters

	mu       sync.Mutex
	closed   bool
	carriers map[*carrier]struct{}
	wg       sync.WaitGroup
}

type carrier struct {
	cancel  context.CancelFunc
	email   string
	level   uint32
	streams atomic.Int32
}

type carrierKey struct{}

// NewServer creates the sing-mux server of an inbound. A disabled server serves
// nothing; its inbound routes carrier destinations like any other.
func NewServer(ctx context.Context, options Options) *Server {
	s := newServer(options)
	core.RequireFeatures(ctx, func(d routing.Dispatcher, pm policy.Manager) {
		s.dispatcher = d
		s.policy = pm
	})
	return s
}

func newServer(options Options) *Server {
	if options.MaxStreams == 0 {
		options.MaxStreams = DefaultMaxStreams
	}
	s := &Server{options: options, carriers: make(map[*carrier]struct{})}
	service, err := sm.NewService(sm.ServiceOptions{
		NewStreamContext: func(ctx context.Context, _ net.Conn) context.Context { return streamContext(ctx) },
		Logger:           logger{},
		HandlerEx:        handler{s},
	})
	common.Must(err)
	s.service = service
	return s
}

// Enabled reports whether the inbound accepts sing-mux carriers.
func (s *Server) Enabled() bool {
	return s.options.Enabled
}

// Serve runs one carrier over link and returns when it ends.
func (s *Server) Serve(ctx context.Context, link *transport.Link) error {
	if !s.options.Enabled {
		closeLink(link)
		return errors.New("sing-mux is not enabled on this inbound")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &carrier{cancel: cancel}
	if !s.register(c) {
		closeLink(link)
		return errors.New("sing-mux server closed")
	}
	defer s.unregister(c)

	options := []cnc.ConnectionOption{cnc.ConnectionInputMulti(link.Writer), cnc.ConnectionOutputMulti(link.Reader)}
	var source M.Socksaddr
	if inbound := session.InboundFromContext(ctx); inbound != nil {
		if inbound.Source.IsValid() {
			source = toSocksaddr(inbound.Source)
			if inbound.Source.Address.Family().IsIP() {
				options = append(options, cnc.ConnectionRemoteAddr(&net.TCPAddr{IP: inbound.Source.Address.IP(), Port: int(inbound.Source.Port)}))
			}
		}
		if inbound.User != nil {
			c.email = inbound.User.Email
			c.level = inbound.User.Level
		}
	}
	conn := cnc.NewConnection(options...)
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	deadline := time.AfterFunc(s.policy.ForLevel(c.level).Timeouts.Handshake, func() { conn.Close() })
	head, protocol, padded, err := readPreamble(conn)
	deadline.Stop()
	if err != nil {
		s.stats.reject(rejectPreamble)
		return errors.New("invalid sing-mux preamble").Base(err)
	}
	s.stats.openCarrier(protocol, padded)
	defer s.stats.closeCarrier(protocol, padded)
	errors.LogDebug(ctx, "sing-mux carrier ", protocolNames[protocol], " padding=", padded)
	s.service.NewConnectionEx(context.WithValue(ctx, carrierKey{}, c), &replayConn{Conn: conn, head: head}, source, sm.Destination, nil)
	return nil
}

// Close ends every carrier and waits for their streams.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for c := range s.carriers {
		c.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Server) register(c *carrier) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.carriers[c] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) unregister(c *carrier) {
	s.mu.Lock()
	delete(s.carriers, c)
	s.mu.Unlock()
	s.wg.Done()
}

// admit reserves a stream slot on the carrier, or returns the refusal reason.
func (s *Server) admit(ctx context.Context) (*carrier, string) {
	c, _ := ctx.Value(carrierKey{}).(*carrier)
	s.mu.Lock()
	closed := s.closed || c == nil || ctx.Err() != nil
	if !closed {
		s.wg.Add(1)
	}
	s.mu.Unlock()
	if closed {
		return nil, rejectClosed
	}
	if c.streams.Add(1) > int32(s.options.MaxStreams) {
		s.release(c)
		return nil, rejectLimit
	}
	if s.options.UserActive != nil && c.email != "" && !s.options.UserActive(ctx, c.email) {
		s.release(c)
		return nil, rejectRevoked
	}
	return c, ""
}

func (s *Server) release(c *carrier) {
	c.streams.Add(-1)
	s.wg.Done()
}

// readPreamble reads the session version, protocol and padding flag that open a
// carrier, returning the bytes for the sing-mux service to read again.
func readPreamble(r io.Reader) (head []byte, protocol byte, padded bool, err error) {
	head = make([]byte, 2, 3)
	if _, err = io.ReadFull(r, head); err != nil {
		return nil, 0, false, err
	}
	if head[0] > sm.Version1 || head[1] > sm.ProtocolH2Mux {
		return nil, 0, false, errors.New("unsupported version ", head[0], " or protocol ", head[1])
	}
	if head[0] == sm.Version1 {
		head = head[:3]
		if _, err = io.ReadFull(r, head[2:]); err != nil {
			return nil, 0, false, err
		}
		padded = head[2] != 0
	}
	return head, head[1], padded, nil
}

type replayConn struct {
	net.Conn
	head []byte
}

func (c *replayConn) Read(b []byte) (int, error) {
	if len(c.head) > 0 {
		n := copy(b, c.head)
		c.head = c.head[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

func closeLink(link *transport.Link) {
	common.Interrupt(link.Reader)
	common.Close(link.Writer)
}

// streamContext gives each stream its own session ID, outbounds and content while
// keeping the carrier's inbound tag, source, user and sniffing settings.
func streamContext(ctx context.Context) context.Context {
	inbound := session.Inbound{}
	if old := session.InboundFromContext(ctx); old != nil {
		inbound = *old
	}
	inbound.Timer = nil
	inbound.CanSpliceCopy = 3
	content := session.Content{}
	if old := session.ContentFromContext(ctx); old != nil {
		content.SniffingRequest = old.SniffingRequest
	}
	ctx = xctx.ContextWithID(ctx, session.NewID())
	ctx = session.ContextWithInbound(ctx, &inbound)
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{}})
	return session.ContextWithContent(ctx, &content)
}

func toDestination(address M.Socksaddr, network net.Network) net.Destination {
	if address.IsFqdn() {
		return net.Destination{Network: network, Address: net.DomainAddress(address.Fqdn), Port: net.Port(address.Port)}
	}
	if address.IsIP() {
		return net.Destination{Network: network, Address: net.IPAddress(address.Addr.Unmap().AsSlice()), Port: net.Port(address.Port)}
	}
	return net.Destination{}
}

func toSocksaddr(destination net.Destination) M.Socksaddr {
	var address M.Socksaddr
	if destination.Address.Family().IsDomain() {
		address.Fqdn = destination.Address.Domain()
	} else {
		address.Addr = M.AddrFromIP(destination.Address.IP())
	}
	address.Port = uint16(destination.Port)
	return address
}
