package anytls

import (
	"context"
	"fmt"
	"io"
	stdnet "net"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	engine "github.com/xtls/xray-core/proxy/anytls/internal/engine"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type clientDialKey struct{}
type clientDial struct {
	dialer internet.Dialer
	tags   []string
}

// Client owns physical sessions; Process owns only a logical stream. Pending
// admissions are cancellable on retirement, while admitted streams may drain.
type Client struct {
	ctx     context.Context
	cancel  context.CancelFunc
	server  *protocol.ServerSpec
	policy  policy.Manager
	pool    *engine.Client
	mu      sync.Mutex
	retired bool
	pending map[*clientOperation]struct{}
	active  map[*clientOperation]struct{}
	done    chan struct{}
	finish  sync.Once
}

type clientOperation struct{ cancel context.CancelFunc }

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	return newClient(ctx, config, core.MustFromContext(ctx).GetFeature(policy.ManagerType()).(policy.Manager))
}

func newClient(ctx context.Context, config *ClientConfig, pm policy.Manager) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	server, err := protocol.NewServerSpecFromPB(config.Server)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &Client{ctx: ctx, cancel: cancel, server: server, policy: pm,
		pending: make(map[*clientOperation]struct{}), active: make(map[*clientOperation]struct{}), done: make(chan struct{})}
	c.pool, err = engine.NewClient(engine.ClientOptions{
		Password: server.User.Account.(*MemoryAccount).password, DialOut: c.dial,
		CancelableWrites:         true,
		IdleSessionCheckInterval: clientIdleDuration(config.IdleSessionCheckInterval),
		IdleSessionTimeout:       clientIdleDuration(config.IdleSessionTimeout), MinIdleSession: int(config.MinIdleSession),
		MaxSessions: int(config.MaxSessions), MaxIdleSessions: int(config.MaxIdleSessions), MaxConcurrentDials: int(config.MaxConcurrentDials),
	})
	if err != nil {
		cancel()
		return nil, err
	}
	return c, nil
}

type clientConnection struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *clientConnection) Close() error {
	c.cancel()
	return c.Conn.Close()
}

func (c *Client) dial(request context.Context) (net.Conn, error) {
	options := request.Value(clientDialKey{}).(clientDial)
	// Only immutable route ancestry crosses into the physical session. Inbound
	// users, sniffing state and caller cancellation never become pool state.
	ctx, cancel := context.WithCancel(c.ctx)
	outbounds := make([]*session.Outbound, len(options.tags))
	for i, tag := range options.tags {
		outbounds[i] = &session.Outbound{Tag: tag, Target: c.server.Destination}
	}
	ctx = session.ContextWithOutbounds(ctx, outbounds)
	ctx = session.ContextWithContent(ctx, &session.Content{})
	stop := context.AfterFunc(request, cancel)
	conn, err := options.dialer.Dial(ctx, c.server.Destination)
	if !stop() || request.Err() != nil || err != nil {
		cancel()
		// Core's statistical dialer may wrap a nil connection on dial failure.
		if stat.TryUnwrapStatsConn(conn) != nil {
			conn.Close()
		}
		if err == nil {
			err = request.Err()
		}
		return nil, err
	}
	return &clientConnection{Conn: conn, cancel: cancel}, nil
}

func (c *Client) complete(op *clientOperation) {
	op.cancel()
	c.mu.Lock()
	delete(c.pending, op)
	delete(c.active, op)
	finished := c.retired && len(c.pending) == 0 && len(c.active) == 0
	c.mu.Unlock()
	if finished {
		c.finish.Do(func() {
			c.cancel()
			c.pool.Close()
			close(c.done)
		})
	}
}

func (c *Client) Retire() <-chan struct{} {
	c.mu.Lock()
	c.retired = true
	var pending []*clientOperation
	for op := range c.pending {
		pending = append(pending, op)
	}
	c.mu.Unlock()
	for _, op := range pending {
		op.cancel()
	}
	c.pool.SetKeepIdleConnections(false)
	c.complete(&clientOperation{cancel: func() {}})
	return c.done
}

func (c *Client) Close() error {
	c.Retire()
	c.cancel()
	c.pool.Close()
	c.mu.Lock()
	var operations []*clientOperation
	for op := range c.active {
		operations = append(operations, op)
	}
	c.mu.Unlock()
	for _, op := range operations {
		op.cancel()
	}
	<-c.done
	return nil
}

func (c *Client) Process(parent context.Context, link *transport.Link, dialer internet.Dialer) error {
	obs := session.OutboundsFromContext(parent)
	if len(obs) == 0 || obs[len(obs)-1] == nil || !obs[len(obs)-1].Target.IsValid() {
		return fmt.Errorf("anytls: missing target")
	}
	ob := obs[len(obs)-1]
	destination := ob.Target
	if destination.Network != net.Network_TCP && destination.Network != net.Network_UDP {
		return fmt.Errorf("anytls: unsupported target network")
	}
	if session.MitmServerNameFromContext(parent) != "" || session.MitmAlpn11FromContext(parent) {
		return fmt.Errorf("anytls: request-dependent TLS is incompatible with pooled sessions")
	}
	ob.Name, ob.CanSpliceCopy = "anytls", 3
	tags := make([]string, len(obs))
	seen := make(map[string]bool)
	for i, previous := range obs {
		if previous == nil {
			return fmt.Errorf("anytls: invalid route ancestry")
		}
		tags[i] = previous.Tag
		if previous.Tag != "" && seen[previous.Tag] {
			return fmt.Errorf("anytls: outbound chain cycle")
		}
		seen[previous.Tag] = true
	}
	ctx, cancel := context.WithCancel(parent)
	op := &clientOperation{cancel: cancel}
	c.mu.Lock()
	if c.retired {
		c.mu.Unlock()
		cancel()
		return stdnet.ErrClosed
	}
	c.pending[op] = struct{}{}
	c.mu.Unlock()
	defer c.complete(op)
	p := c.policy.ForLevel(c.server.User.Level)
	dialCtx, dialCancel := context.WithTimeout(ctx, p.Timeouts.Handshake)
	dialCtx = context.WithValue(dialCtx, clientDialKey{}, clientDial{dialer: dialer, tags: tags})
	target := singbridge.ToSocksaddr(destination)
	if destination.Network == net.Network_UDP {
		target = M.Socksaddr{Fqdn: uot.MagicAddress, Port: 443}
	}
	conn, err := c.pool.OpenContext(dialCtx, target)
	dialCancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	c.mu.Lock()
	if c.retired || ctx.Err() != nil {
		c.mu.Unlock()
		return stdnet.ErrClosed
	}
	delete(c.pending, op)
	c.active[op] = struct{}{}
	c.mu.Unlock()
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			conn.Close()
			common.Interrupt(link.Reader)
			common.Interrupt(link.Writer)
		})
	}
	stop := context.AfterFunc(ctx, cleanup)
	graceful := false
	timer := signal.CancelAfterInactivity(ctx, cancel, p.Timeouts.ConnectionIdle)
	defer func() {
		stopped := stop()
		// SetTimeout(0) invokes cancel, so detach the abort callback first.
		timer.SetTimeout(0)
		if !graceful || !stopped {
			cleanup()
		}
	}()
	var reader buf.Reader = buf.NewReader(conn)
	var writer buf.Writer = buf.NewWriter(conn)
	if destination.Network == net.Network_UDP {
		request := uot.Request{Destination: singbridge.ToSocksaddr(destination)}
		conn.SetWriteDeadline(time.Now().Add(p.Timeouts.Handshake))
		err = uot.WriteRequest(conn, request)
		conn.SetWriteDeadline(time.Time{})
		if err != nil {
			return err
		}
		packets := &clientPackets{conn: uot.NewConn(conn, request), target: destination}
		reader, writer = packets, packets
	}
	done := make(chan error, 2)
	go func() {
		err := buf.Copy(link.Reader, writer, buf.UpdateActivity(timer))
		timer.SetTimeout(p.Timeouts.DownlinkOnly)
		done <- err
	}()
	go func() {
		err := buf.Copy(reader, link.Writer, buf.UpdateActivity(timer))
		common.Close(link.Writer)
		timer.SetTimeout(p.Timeouts.UplinkOnly)
		done <- err
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
		err = nil
	}
	graceful = err == nil && ctx.Err() == nil
	return err
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
