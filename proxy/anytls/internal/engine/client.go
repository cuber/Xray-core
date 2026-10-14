package anytls

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/x/list"
)

type keepSessionKey struct{}

func ContextWithKeepSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, (*keepSessionKey)(nil), true)
}

func keepSessionFromContext(ctx context.Context) bool {
	keep, _ := ctx.Value((*keepSessionKey)(nil)).(bool)
	return keep
}

type ClientOptions struct {
	Password                 string
	ClientMetadata           string
	DialOut                  DialOutFunc
	IdleSessionCheckInterval time.Duration
	IdleSessionTimeout       time.Duration
	MinIdleSession           int
	MaxSessions              int
	MaxIdleSessions          int
	MaxConcurrentDials       int
	DisableReuse             bool
	// Core's native outbound must join canceled writes even on dispatch-backed
	// connections whose SetWriteDeadline is a no-op. Other adapters opt in.
	CancelableWrites bool
	Logger           logger.ContextLogger
}

type Client struct {
	password       [passwordLen]byte
	clientMetadata string
	dialOut        DialOutFunc
	logger         logger.ContextLogger
	padding        common.TypedValue[*paddingFactory]

	idleCheckInterval  time.Duration
	idleTimeout        time.Duration
	minIdleSession     int
	maxSessions        int
	maxIdleSessions    int
	maxConcurrentDials int
	creating           int
	disableReuse       bool
	cancelableWrites   bool
	closeIdle          atomic.Bool

	access       sync.Mutex
	closed       bool
	sessions     map[*session]struct{}
	idleSessions list.List[*session]
	idleTimer    *time.Timer
	readers      sync.WaitGroup
	dials        sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewClient(options ClientOptions) (*Client, error) {
	if err := options.ValidateLimits(); err != nil {
		return nil, err
	}
	factory, err := newPaddingFactory(DefaultPaddingScheme)
	if err != nil {
		return nil, err
	}
	client := &Client{
		password:           sha256.Sum256([]byte(options.Password)),
		clientMetadata:     options.ClientMetadata,
		dialOut:            options.DialOut,
		logger:             options.Logger,
		idleCheckInterval:  options.IdleSessionCheckInterval,
		idleTimeout:        options.IdleSessionTimeout,
		minIdleSession:     options.MinIdleSession,
		maxSessions:        options.MaxSessions,
		maxIdleSessions:    options.MaxIdleSessions,
		maxConcurrentDials: options.MaxConcurrentDials,
		disableReuse:       options.DisableReuse,
		cancelableWrites:   options.CancelableWrites,
		sessions:           make(map[*session]struct{}),
	}
	client.ctx, client.cancel = context.WithCancel(context.Background())
	if client.logger == nil {
		client.logger = logger.NOP()
	}
	if client.idleCheckInterval <= minimumIdleSessionInterval {
		client.idleCheckInterval = defaultIdleSessionCheckInterval
	}
	if client.idleTimeout <= minimumIdleSessionInterval {
		client.idleTimeout = defaultIdleSessionTimeout
	}
	client.padding.Store(factory)
	return client, nil
}

// ValidateLimits applies finite defaults without adding a waiting queue.
func (o *ClientOptions) ValidateLimits() error {
	if o.MaxSessions == 0 {
		o.MaxSessions = 256
	}
	if o.MaxIdleSessions == 0 {
		o.MaxIdleSessions = min(64, o.MaxSessions)
	}
	if o.MaxConcurrentDials == 0 {
		o.MaxConcurrentDials = min(64, o.MaxSessions)
	}
	if o.MaxSessions < 1 || o.MaxSessions > 4096 || o.MaxIdleSessions < 1 || o.MaxIdleSessions > o.MaxSessions || o.MaxConcurrentDials < 1 || o.MaxConcurrentDials > o.MaxSessions || o.MinIdleSession < 0 || o.MinIdleSession > o.MaxIdleSessions {
		return E.New("anytls: invalid session pool limits")
	}
	return nil
}

// OpenContext completes the SYN/destination write before admission. It never
// retries application data, and cancellation cannot leave a half-open pooled
// session. DialContext remains lazy for existing callers.
func (c *Client) OpenContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	conn, err := c.DialContext(ctx, destination)
	if err != nil {
		return nil, err
	}
	opened := conn.(*stream)
	stop := context.AfterFunc(ctx, func() { opened.session.Close() })
	err = opened.handshake(&opened.writeDeadline)
	if !stop() || ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		opened.session.Close()
		opened.Close()
		return nil, err
	}
	return conn, nil
}

func (c *Client) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	if !destination.IsValid() {
		return nil, E.New("anytls: invalid destination: ", destination)
	}
	if len(destination.Fqdn) > 255 {
		return nil, E.New("anytls: domain too long: ", destination.Fqdn)
	}
	keepSession := keepSessionFromContext(ctx)
	idle := c.takeIdleSession()
	if idle != nil {
		opened, err := idle.openStream(destination)
		c.finishReservation(idle)
		if err == nil && !idle.IsClosed() {
			if keepSession {
				idle.keepOnce.Store(true)
			}
			return opened, nil
		}
		if opened != nil {
			opened.Close()
		}
		idle.Close()
	}
	created, err := c.createSession(ctx)
	if err != nil {
		return nil, err
	}
	opened, err := created.openStream(destination)
	c.finishReservation(created)
	if err != nil {
		created.Close()
		return nil, err
	}
	if keepSession {
		created.keepOnce.Store(true)
	}
	return opened, nil
}

func (c *Client) createSession(ctx context.Context) (*session, error) {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		return nil, net.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		c.access.Unlock()
		return nil, err
	}
	if len(c.sessions)+c.creating >= c.maxSessions || c.creating >= c.maxConcurrentDials {
		c.access.Unlock()
		return nil, E.New("anytls: session pool limit")
	}
	c.creating++
	c.dials.Add(1)
	c.access.Unlock()
	reserved := true
	defer func() {
		if reserved {
			c.access.Lock()
			c.creating--
			c.access.Unlock()
		}
		c.dials.Done()
	}()
	ctx, cancel := context.WithCancel(ctx)
	stopClose := context.AfterFunc(c.ctx, cancel)
	defer func() { stopClose(); cancel() }()
	conn, err := c.dialOut(ctx)
	if err != nil {
		return nil, err
	}
	// Authentication writes (including a lazy TLS handshake) must be cancellable,
	// but a published session must no longer belong to this first request.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	paddingLen := c.padding.Load().GenerateHandshakePaddingSize()
	if paddingLen < 0 || paddingLen > maxPaddingSize {
		conn.Close()
		return nil, ErrPaddingScheme
	}
	request := buf.NewSize(passwordLen + 2 + paddingLen)
	common.Must1(request.Write(c.password[:]))
	binary.BigEndian.PutUint16(request.Extend(2), uint16(paddingLen))
	common.Must(request.WriteZeroN(paddingLen))
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		conn.SetWriteDeadline(deadline)
	}
	_, err = conn.Write(request.Bytes())
	request.Release()
	if err != nil {
		conn.Close()
		return nil, E.Cause(err, "anytls: write request")
	}
	conn.SetWriteDeadline(time.Time{})
	if !stop() || ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}
	created := newClientSession(c, conn)
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		created.Close()
		return nil, net.ErrClosed
	}
	c.sessions[created] = struct{}{}
	c.creating--
	reserved = false
	created.reserved = true
	c.readers.Add(1)
	c.access.Unlock()
	go func() {
		defer c.readers.Done()
		loopErr := created.readLoop()
		created.Close()
		if !E.IsClosedOrCanceled(loopErr) {
			c.logger.Debug(E.Cause(loopErr, "anytls: session closed"))
		}
	}()
	return created, nil
}

func (c *Client) takeIdleSession() *session {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		return nil
	}
	for element := c.idleSessions.Front(); element != nil; element = element.Next() {
		idle := element.Value
		c.idleSessions.Remove(element)
		idle.element = nil
		idle.reserved = true
		return idle
	}
	return nil
}

func (c *Client) finishReservation(s *session) {
	c.access.Lock()
	s.reserved = false
	c.access.Unlock()
}

func (c *Client) beginControl(s *session) {
	c.access.Lock()
	s.controls++
	if s.element != nil {
		c.idleSessions.Remove(s.element)
		s.element = nil
	}
	c.access.Unlock()
}

func (c *Client) endControl(s *session) {
	c.access.Lock()
	s.controls--
	c.access.Unlock()
	c.releaseSession(s)
}

func (c *Client) releaseSession(released *session) {
	c.access.Lock()
	if released.IsClosed() || c.closed {
		if released.element != nil {
			c.idleSessions.Remove(released.element)
			released.element = nil
		}
		delete(c.sessions, released)
		c.access.Unlock()
		return
	}
	if released.reserved || released.controls != 0 {
		c.access.Unlock()
		return
	}
	if c.disableReuse {
		c.access.Unlock()
		released.Close()
		return
	}
	keepOnce := released.keepOnce.Swap(false)
	if !released.hasStreams() && c.closeIdle.Load() && !keepOnce {
		if released.element != nil {
			c.idleSessions.Remove(released.element)
			released.element = nil
		}
		c.access.Unlock()
		released.Close()
		return
	}
	if !released.hasStreams() && (!c.cancelableWrites || len(released.writeAccess) == 0) && released.element == nil {
		if c.idleSessions.Len() >= c.maxIdleSessions {
			// Keep it counted until Close removes it; no replacement may overbook.
			c.access.Unlock()
			released.Close()
			return
		}
		released.idleSince = time.Now()
		released.element = c.idleSessions.PushFront(released)
		if c.idleTimer == nil {
			c.idleTimer = time.AfterFunc(c.idleCheckInterval, c.cleanupIdleSessions)
		}
	}
	c.access.Unlock()
}

func (c *Client) SetKeepIdleConnections(keep bool) {
	c.closeIdle.Store(!keep)
	if !keep {
		c.CloseIdleConnections()
	}
}

func (c *Client) CloseIdleConnections() {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		return
	}
	var closing []*session
	for element := c.idleSessions.Front(); element != nil; {
		idle := element.Value
		element = element.Next()
		if idle.hasStreams() {
			continue
		}
		c.idleSessions.Remove(idle.element)
		idle.element = nil
		closing = append(closing, idle)
	}
	c.access.Unlock()
	for _, idle := range closing {
		idle.Close()
	}
}

func (c *Client) Reset() {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		return
	}
	sessions := make([]*session, 0, len(c.sessions))
	for closing := range c.sessions {
		closing.element = nil
		sessions = append(sessions, closing)
	}
	clear(c.sessions)
	c.idleSessions.Init()
	c.access.Unlock()
	for _, closing := range sessions {
		closing.Close()
	}
}

func (c *Client) removeSession(removed *session) {
	c.access.Lock()
	defer c.access.Unlock()
	if removed.element != nil {
		c.idleSessions.Remove(removed.element)
		removed.element = nil
	}
	delete(c.sessions, removed)
}

func (c *Client) cleanupIdleSessions() {
	var expired []*session
	c.access.Lock()
	if c.closed {
		c.idleTimer = nil
		c.access.Unlock()
		return
	}
	deadline := time.Now().Add(-c.idleTimeout)
	for element := c.idleSessions.Back(); element != nil && c.idleSessions.Len() > c.minIdleSession; {
		previous := element.Prev()
		idle := element.Value
		if !idle.idleSince.Before(deadline) {
			break
		}
		if idle.hasStreams() {
			element = previous
			continue
		}
		c.idleSessions.Remove(element)
		idle.element = nil
		expired = append(expired, idle)
		element = previous
	}
	if c.idleSessions.Len() > 0 {
		c.idleTimer.Reset(c.idleCheckInterval)
	} else {
		c.idleTimer = nil
	}
	c.access.Unlock()
	for _, idle := range expired {
		idle.Close()
	}
}

func (c *Client) Close() error {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		c.dials.Wait()
		c.readers.Wait()
		return nil
	}
	c.closed = true
	c.cancel()
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	sessions := make([]*session, 0, len(c.sessions))
	for closing := range c.sessions {
		closing.element = nil
		sessions = append(sessions, closing)
	}
	clear(c.sessions)
	c.idleSessions.Init()
	c.access.Unlock()
	var err error
	for _, closing := range sessions {
		err = E.Errors(err, closing.Close())
	}
	c.dials.Wait()
	c.readers.Wait()
	return err
}
