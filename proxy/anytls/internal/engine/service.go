package anytls

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type ServiceOptions struct {
	PaddingScheme    []byte
	Handler          N.TCPConnectionHandlerEx
	FallbackHandler  N.TCPConnectionHandlerEx
	Logger           logger.ContextLogger
	Authenticate     func(context.Context, []byte) (context.Context, bool)
	StreamOpen       func(context.Context) bool
	StreamClose      func(context.Context)
	HandshakeTimeout func(context.Context) time.Duration
	SessionReady     func(context.Context)
	SessionClosed    func(context.Context)
}

type Service struct {
	options         ServiceOptions
	password        [passwordLen]byte
	padding         common.TypedValue[*paddingFactory]
	handler         N.TCPConnectionHandlerEx
	fallbackHandler N.TCPConnectionHandlerEx
	logger          logger.ContextLogger
}

func newService(options ServiceOptions) (*Service, error) {
	paddingScheme := options.PaddingScheme
	if len(paddingScheme) == 0 {
		paddingScheme = DefaultPaddingScheme
	}
	factory, err := newPaddingFactory(paddingScheme)
	if err != nil {
		return nil, err
	}
	service := &Service{
		options:         options,
		handler:         options.Handler,
		fallbackHandler: options.FallbackHandler,
		logger:          options.Logger,
	}
	if service.logger == nil {
		service.logger = logger.NOP()
	}
	service.padding.Store(factory)
	return service, nil
}

func NewService(password string, options ServiceOptions) (*Service, error) {
	service, err := newService(options)
	if err != nil {
		return nil, err
	}
	service.password = sha256.Sum256([]byte(password))
	return service, nil
}

func (s *Service) UpdatePaddingScheme(rawScheme []byte) error {
	factory, err := newPaddingFactory(rawScheme)
	if err != nil {
		return err
	}
	s.padding.Store(factory)
	return nil
}

// sing-anytls 0.0.13 Service.NewConnection reads the prologue with a single ReadOnceFrom
// and hands whatever arrived to the fallback handler, so a peer that speaks a
// server-first protocol is never kept waiting for 32 bytes it will not send.
func (s *Service) handleConnection(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc, authenticate func(password []byte) (context.Context, bool)) error {
	buffer := buf.NewPacket()
	requestLen, err := buffer.ReadOnceFrom(conn)
	if err != nil {
		buffer.Release()
		conn.Close()
		return E.Cause(err, "anytls: read request")
	}
	password, err := buffer.ReadBytes(passwordLen)
	var serveContext context.Context
	var authenticated bool
	if err == nil {
		serveContext, authenticated = authenticate(password)
	}
	if !authenticated {
		buffer.Resize(0, requestLen)
	}
	// NewCachedConn takes a reference; from here the cached connection owns the
	// buffer and releases it when it is drained or closed.
	cachedConn := bufio.NewCachedConn(conn, buffer)
	buffer.Release()
	if !authenticated {
		return s.fallback(ctx, cachedConn, source, ErrAuthentication, onClose)
	}
	return s.serve(serveContext, cachedConn, source, onClose)
}

func (s *Service) NewConnection(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	if s.options.Authenticate != nil {
		var password [passwordLen]byte
		if _, err := io.ReadFull(conn, password[:]); err != nil {
			return err
		}
		ctx, ok := s.options.Authenticate(ctx, password[:])
		if !ok {
			return ErrAuthentication
		}
		return s.serve(ctx, conn, source, onClose)
	}
	return s.handleConnection(ctx, conn, source, onClose, func(password []byte) (context.Context, bool) {
		if [passwordLen]byte(password) != s.password {
			return nil, false
		}
		return ctx, true
	})
}

func (s *Service) fallback(ctx context.Context, conn net.Conn, source M.Socksaddr, cause error, onClose N.CloseHandlerFunc) error {
	if s.fallbackHandler == nil {
		conn.Close()
		return E.Errors(cause, ErrFallbackClosed)
	}
	s.fallbackHandler.NewConnectionEx(ctx, conn, source, M.Socksaddr{}, onClose)
	return nil
}

func (s *Service) serve(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	var paddingLenBytes [2]byte
	_, err := io.ReadFull(conn, paddingLenBytes[:])
	if err != nil {
		conn.Close()
		return E.Cause(err, "anytls: read padding length")
	}
	paddingLen := int(binary.BigEndian.Uint16(paddingLenBytes[:]))
	if paddingLen > 0 {
		padding := buf.NewSize(paddingLen)
		_, err = padding.ReadFullFrom(conn, paddingLen)
		padding.Release()
		if err != nil {
			conn.Close()
			return E.Cause(err, "anytls: read padding")
		}
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if s.options.SessionReady != nil {
		s.options.SessionReady(ctx)
	}
	serving := newServerSession(ctx, s, conn, source)
	err = serving.readLoop()
	if s.options.SessionClosed != nil {
		s.options.SessionClosed(ctx)
	}
	serving.Close()
	serving.streamWG.Wait()
	if E.IsClosedOrCanceled(err) {
		err = nil
	} else {
		s.logger.DebugContext(ctx, E.Cause(err, "anytls: session closed"))
	}
	if onClose != nil {
		onClose(err)
	}
	return nil
}

func (s *Service) acceptStream(parent *session, accepted *stream) {
	defer parent.streamWG.Done()
	if s.options.StreamClose != nil {
		defer s.options.StreamClose(parent.serviceContext)
	}
	defer accepted.Close()
	if s.options.HandshakeTimeout != nil {
		if timeout := s.options.HandshakeTimeout(parent.serviceContext); timeout > 0 {
			accepted.SetReadDeadline(time.Now().Add(timeout))
		}
	}
	ctx := parent.serviceContext
	destination, err := M.SocksaddrSerializer.ReadAddrPort(accepted)
	if err != nil {
		if !E.IsClosedOrCanceled(err) {
			s.logger.ErrorContext(ctx, E.Cause(err, "anytls: read destination"))
		}
		N.CloseOnHandshakeFailure(accepted, nil, err)
		return
	}
	accepted.SetReadDeadline(time.Time{})
	s.handler.NewConnectionEx(ctx, accepted, parent.source, destination, nil)
}

type MultiService[U comparable] struct {
	*Service
	users common.TypedValue[map[[passwordLen]byte]U]
}

func NewMultiService[U comparable](options ServiceOptions) (*MultiService[U], error) {
	service, err := newService(options)
	if err != nil {
		return nil, err
	}
	multiService := &MultiService[U]{Service: service}
	multiService.users.Store(make(map[[passwordLen]byte]U))
	return multiService, nil
}

func (s *MultiService[U]) UpdateUsers(users []U, passwords []string) error {
	userMap := make(map[[passwordLen]byte]U, len(users))
	for index, user := range users {
		userMap[sha256.Sum256([]byte(passwords[index]))] = user
	}
	s.users.Store(userMap)
	return nil
}

func (s *MultiService[U]) NewConnection(ctx context.Context, conn net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	return s.handleConnection(ctx, conn, source, onClose, func(password []byte) (context.Context, bool) {
		user, loaded := s.users.Load()[[passwordLen]byte(password)]
		if !loaded {
			return nil, false
		}
		return auth.ContextWithUser(ctx, user), true
	})
}
