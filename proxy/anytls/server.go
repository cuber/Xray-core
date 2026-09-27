package anytls

import (
	"context"
	stdtls "crypto/tls"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xctx "github.com/xtls/xray-core/common/ctx"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/internet/tls"
)

type userRecord struct {
	user     protocol.MemoryUser
	hash     [32]byte
	sessions int
	revoked  bool
}

// Reserve 256 KiB per active handler for protocol buffers (pending, cached,
// in-flight frames and UoT input). Dispatcher buffers have a separate cap.
const maxActiveStreams = (32 * 1024 * 1024) / (256 * 1024)

const maxUDPLinks = 128
const maxPipeBuffer = 32 * 1024

func boundedBufferPolicy(p policy.Buffer) policy.Buffer {
	if p.PerConnection < 0 || p.PerConnection > maxPipeBuffer {
		p.PerConnection = maxPipeBuffer
	}
	return p
}

type connectionKey struct{}
type connection struct {
	net.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	user             *userRecord
	streams          int
	streamCount      uint64
	retiring         bool
	monitorDone      chan struct{}
	timeout          atomic.Int64
	admissionFailure string
}

func (c *connection) touch() {
	if d := c.timeout.Load(); d > 0 {
		c.Conn.SetReadDeadline(time.Now().Add(time.Duration(d)))
	}
}
func (c *connection) Read(p []byte) (int, error) {
	c.touch()
	return c.Conn.Read(p)
}

func (c *connection) Write(p []byte) (int, error) {
	c.touch()
	return c.Conn.Write(p)
}

func (c *connection) close() {
	c.cancel()
	c.Conn.Close()
}

type Server struct {
	mu                                  sync.Mutex
	users                               map[string]*userRecord
	passwords                           map[[32]byte]*userRecord
	connections                         map[*connection]struct{}
	closed                              bool
	activeStreams                       int
	activeUDPLinks                      int
	wg                                  sync.WaitGroup
	service                             *engine.Service
	policy                              policy.Manager
	maxSessions, maxPerUser, maxStreams int
	lastAdmissionWarning                time.Time
}

var _ proxy.Inbound = (*Server)(nil)
var _ proxy.UserManager = (*Server)(nil)

func NewServer(ctx context.Context, c *ServerConfig) (*Server, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	pm := core.MustFromContext(ctx).GetFeature(policy.ManagerType()).(policy.Manager)
	return newServer(c, pm)
}
func newServer(c *ServerConfig, pm policy.Manager) (*Server, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s := &Server{
		users:       map[string]*userRecord{},
		passwords:   map[[32]byte]*userRecord{},
		connections: map[*connection]struct{}{},
		policy:      pm,
		maxSessions: int(c.MaxSessions),
		maxPerUser:  int(c.MaxSessionsPerUser),
		maxStreams:  int(c.MaxStreamsPerSession),
	}
	if s.maxSessions == 0 {
		s.maxSessions = 256
	}
	if s.maxStreams == 0 {
		s.maxStreams = 128
	}
	var err error
	s.service, err = engine.NewService("unused-custom-auth", engine.ServiceOptions{
		Handler:       s,
		PaddingScheme: []byte(strings.Join(c.PaddingScheme, "\n")),
		Authenticate:  s.authenticate,
		StreamOpen:    s.openStream,
		StreamClose:   s.closeStream,
		HandshakeTimeout: func(ctx context.Context) time.Duration {
			c := ctx.Value(connectionKey{}).(*connection)
			return pm.ForLevel(c.user.user.Level).Timeouts.Handshake
		},
		SessionReady: func(ctx context.Context) {
			v := ctx.Value(connectionKey{}).(*connection)
			v.timeout.Store(int64(pm.ForLevel(v.user.user.Level).Timeouts.ConnectionIdle))
			v.monitorDone = make(chan struct{})
			go func() {
				defer close(v.monitorDone)
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				s.monitorIdle(v, ticker.C)
			}()
		},
		SessionClosed: func(ctx context.Context) {
			ctx.Value(connectionKey{}).(*connection).cancel()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("anytls: invalid protocol configuration")
	}
	for _, u := range c.Users {
		m, _ := u.ToMemoryUser()
		if err = s.AddUser(context.Background(), m); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Server) AddUser(_ context.Context, u *protocol.MemoryUser) error {
	if u == nil || u.Email == "" {
		return fmt.Errorf("anytls: email is required")
	}
	a, ok := u.Account.(*MemoryAccount)
	if !ok || a.password == "" {
		return fmt.Errorf("anytls: invalid account")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("anytls: server closed")
	}
	if s.users[u.Email] != nil || s.passwords[a.hash] != nil {
		return fmt.Errorf("anytls: user or password already exists")
	}
	account := *a
	snapshot := *u
	snapshot.Account = &account
	r := &userRecord{user: snapshot, hash: a.hash}
	s.users[u.Email] = r
	s.passwords[a.hash] = r
	return nil
}
func (s *Server) RemoveUser(_ context.Context, email string) error {
	s.mu.Lock()
	r := s.users[email]
	if r == nil {
		s.mu.Unlock()
		return fmt.Errorf("anytls: user not found")
	}
	r.revoked = true
	delete(s.users, email)
	delete(s.passwords, r.hash)
	var conns []*connection
	for c := range s.connections {
		if c.user == r {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	return nil
}
func cloneUser(u protocol.MemoryUser) *protocol.MemoryUser {
	a := *(u.Account.(*MemoryAccount))
	u.Account = &a
	return &u
}
func (s *Server) GetUser(_ context.Context, email string) *protocol.MemoryUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.users[email]; r != nil {
		return cloneUser(r.user)
	}
	return nil
}
func (s *Server) GetUsers(_ context.Context) []*protocol.MemoryUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*protocol.MemoryUser, 0, len(s.users))
	for _, r := range s.users {
		out = append(out, cloneUser(r.user))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}
func (s *Server) GetUsersCount(context.Context) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.users))
}
func (s *Server) authenticate(ctx context.Context, password []byte) (context.Context, bool) {
	if len(password) != 32 {
		return nil, false
	}
	c := ctx.Value(connectionKey{}).(*connection)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.passwords[[32]byte(password)]
	if s.closed || c.ctx.Err() != nil || c.user != nil || r == nil || r.revoked {
		return nil, false
	}
	if s.maxPerUser > 0 && r.sessions >= s.maxPerUser {
		idle := 0
		for other := range s.connections {
			if other.user == r && other.streams == 0 {
				idle++
			}
		}
		c.admissionFailure = fmt.Sprintf("user session limit: user=%q sessions=%d limit=%d idle=%d connections=%d", r.user.Email, r.sessions, s.maxPerUser, idle, len(s.connections))
		return nil, false
	}
	c.user = r
	r.sessions++
	return ctx, true
}
func (s *Server) openStream(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := ctx.Value(connectionKey{}).(*connection)
	if s.closed || c.retiring || c.ctx.Err() != nil || c.user == nil || c.user.revoked || c.streams >= s.maxStreams || s.activeStreams >= maxActiveStreams {
		return false
	}
	c.streams++
	c.streamCount++
	s.activeStreams++
	return true
}
func (s *Server) closeStream(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.Value(connectionKey{}).(*connection).streams--
	s.activeStreams--
}
func (s *Server) Network() []net.Network { return []net.Network{net.Network_TCP} }
func (s *Server) ValidateStream(m *internet.MemoryStreamConfig) error {
	if m == nil || m.ProtocolName != "tcp" || m.SecurityType != "xray.transport.internet.tls.Config" {
		return fmt.Errorf("anytls requires RAW TCP and TLS")
	}
	config, ok := m.SecuritySettings.(*tls.Config)
	if !ok || config == nil {
		return fmt.Errorf("anytls requires TLS certificates")
	}
	usable := false
	for _, cert := range config.Certificate {
		if cert == nil {
			return fmt.Errorf("anytls: invalid TLS certificate")
		}
		if cert.Usage == tls.Certificate_AUTHORITY_VERIFY {
			continue
		}
		if _, err := stdtls.X509KeyPair(cert.Certificate, cert.Key); err != nil {
			return fmt.Errorf("anytls: invalid TLS certificate or private key")
		}
		usable = true
	}
	if !usable {
		return fmt.Errorf("anytls requires a server TLS certificate")
	}
	return nil
}
func (s *Server) Process(ctx context.Context, network net.Network, conn stat.Connection, d routing.Dispatcher) error {
	if network != net.Network_TCP {
		return fmt.Errorf("anytls requires TCP")
	}
	if _, ok := stat.TryUnwrapStatsConn(conn).(*tls.Conn); !ok {
		return fmt.Errorf("anytls requires TLS")
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &connection{Conn: conn, ctx: ctx, cancel: cancel}
	s.mu.Lock()
	if s.closed || len(s.connections) >= s.maxSessions {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("anytls: session limit")
	}
	s.connections[c] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() {
		c.close()
		if c.monitorDone != nil {
			<-c.monitorDone
		}
		s.mu.Lock()
		delete(s.connections, c)
		if c.user != nil {
			c.user.sessions--
		}
		s.mu.Unlock()
		s.wg.Done()
	}()
	ctx = context.WithValue(session.ContextWithDispatcher(ctx, d), connectionKey{}, c)
	if err := conn.SetReadDeadline(time.Now().Add(s.policy.ForLevel(0).Timeouts.Handshake)); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	err := s.service.NewConnection(ctx, c, M.SocksaddrFromNet(conn.RemoteAddr()), nil)
	s.mu.Lock()
	report := c.admissionFailure != "" && time.Since(s.lastAdmissionWarning) >= 5*time.Second
	if report {
		s.lastAdmissionWarning = time.Now()
	}
	s.mu.Unlock()
	if report {
		errors.LogWarning(ctx, "anytls: ", c.admissionFailure)
	}
	return err
}
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	list := make([]*connection, 0, len(s.connections))
	for c := range s.connections {
		list = append(list, c)
	}
	s.mu.Unlock()
	for _, c := range list {
		c.close()
	}
	s.wg.Wait()
	return nil
}

// Only immutable listener metadata is inherited by multiplexed requests.
func streamContext(ctx context.Context, user protocol.MemoryUser, conn net.Conn) context.Context {
	inbound := session.Inbound{}
	if old := session.InboundFromContext(ctx); old != nil {
		inbound = *old
	}
	inbound.User = cloneUser(user)
	inbound.Name = "anytls"
	inbound.Conn = conn
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
func (s *Server) NewConnectionEx(ctx context.Context, conn net.Conn, source, dest M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer conn.Close()
	c := ctx.Value(connectionKey{}).(*connection)
	s.mu.Lock()
	valid := !s.closed && !c.user.revoked && c.ctx.Err() == nil
	u := c.user.user
	s.mu.Unlock()
	if !valid {
		return
	}
	ctx = streamContext(ctx, u, conn)
	ctx = policy.ContextWithBufferPolicy(ctx, boundedBufferPolicy(s.policy.ForLevel(u.Level).Buffer))
	if dest.Fqdn == uot.MagicAddress {
		if err := N.ReportHandshakeSuccess(conn); err != nil {
			return
		}
		s.serveUDP(ctx, conn)
		return
	}
	if dest.Fqdn == uot.LegacyMagicAddress || dest.Fqdn == "v1.mux.cool" || !dest.IsValid() || dest.Port == 0 {
		return
	}
	target := singbridge.ToDestination(dest, net.Network_TCP)
	ctx = log.ContextWithAccessMessage(ctx, &log.AccessMessage{From: source, To: target, Status: log.AccessAccepted, Email: u.Email})
	s.copyTCP(ctx, conn, target)
}
func (s *Server) copyTCP(ctx context.Context, conn net.Conn, dest net.Destination) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	link, err := session.DispatcherFromContext(ctx).Dispatch(ctx, dest)
	if err != nil {
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
	// A reused AnyTLS session requires SYNACK even when application data flows;
	// otherwise independent clients can expire their stream-open watchdog.
	if err := N.ReportHandshakeSuccess(conn); err != nil {
		return err
	}
	p := s.policy.ForLevel(session.InboundFromContext(ctx).User.Level)
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
func init() {
	common.Must(common.RegisterConfig((*ServerConfig)(nil), func(ctx context.Context, c interface{}) (interface{}, error) {
		return NewServer(ctx, c.(*ServerConfig))
	}))
}
