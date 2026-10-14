package shadowsocks_2022

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
)

func (s *ServerUDPSession) UpdateConn(conn stat.Connection) {
	s.Lock()
	defer s.Unlock()
	if !s.closed {
		s.currentConn.Store(&udpClientConn{conn})
		if s.timer != nil {
			s.timer.Update()
		}
	}
}

func (s *ServerUDPSession) WriteToClient(b []byte) error {
	connVal := s.currentConn.Load()
	if connVal == nil {
		return errors.New("client connection closed")
	}
	_, err := connVal.conn.Write(b)
	return err
}

func (s *ServerUDPSession) Close() {
	s.Lock()
	if s.closed {
		s.Unlock()
		return
	}
	s.closed = true
	timer, cancel := s.timer, s.cancel
	if s.expiry != nil {
		s.expiry.Stop()
	}
	s.currentConn.Store(nil)
	link := s.link.Swap(nil)
	if s.manager != nil {
		s.manager.sessions.CompareAndDelete(s.SessionID, s)
	}
	s.Unlock()
	if cancel != nil {
		cancel()
	}
	if timer != nil {
		timer.SetTimeout(0)
	}
	if link != nil {
		common.Interrupt(link.Reader)
		common.Interrupt(link.Writer)
	}
}

func (s *ServerUDPSession) EnsureLink(
	ctx context.Context,
	conn stat.Connection,
	dest net.Destination,
	dispatcher routing.Dispatcher,
	policyManager policy.Manager,
	responseEncoder func(dest net.Destination, payload []byte) ([]byte, error),
) (*transport.Link, error) {
	s.Lock()
	if s.closed {
		s.Unlock()
		return nil, ErrInvalidRequest
	}

	if link := s.link.Load(); link != nil {
		s.currentConn.Store(&udpClientConn{conn})
		s.timer.Update()
		s.Unlock()
		return link, nil
	}
	if pending := s.initializing; pending != nil {
		s.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
			return s.EnsureLink(ctx, conn, dest, dispatcher, policyManager, responseEncoder)
		}
	}
	s.initializing = make(chan struct{})

	s.currentConn.Store(&udpClientConn{conn})

	sessCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	inbound := session.InboundFromContext(sessCtx)
	if inbound != nil && s.User != nil {
		copyInbound := *inbound
		copyInbound.User = s.User
		sessCtx = session.ContextWithInbound(sessCtx, &copyInbound)
	}
	var email string
	var level uint32
	if s.User != nil {
		email = s.User.Email
		level = s.User.Level
	}
	sessCtx = log.ContextWithAccessMessage(sessCtx, &log.AccessMessage{
		From:   conn.RemoteAddr(),
		To:     dest,
		Status: log.AccessAccepted,
		Email:  email,
	})

	// Dispatch may block on routing/dialing. Close must be able to cancel it
	// without waiting for the session mutex, and a late link must not revive it.
	s.Unlock()
	link, err := dispatcher.Dispatch(sessCtx, dest)
	s.Lock()
	defer s.Unlock()
	close(s.initializing)
	s.initializing = nil
	if err != nil || s.closed {
		cancel()
		if link != nil {
			common.Interrupt(link.Reader)
			common.Interrupt(link.Writer)
		}
		if err == nil {
			err = ErrInvalidRequest
		}
		return nil, err
	}

	sessionPolicy := policyManager.ForLevel(level)
	// The timer can fire synchronously for a zero idle policy; do not reenter s.Lock.
	s.timer = signal.CancelAfterInactivity(sessCtx, func() { go s.Close() }, sessionPolicy.Timeouts.ConnectionIdle)
	s.link.Store(link)

	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		handleUDPResponse(s, link, dest, responseEncoder)
	}()
	return link, nil
}

// ResetTCPConn sets SO_LINGER to 0 per SIP022 §3.1.4 to consistently send RST on close
// when handshake or header validation fails.
func ResetTCPConn(conn net.Conn) {
	rawConn, _, _ := proxy.UnwrapRawConn(conn)
	if tcpConn, ok := rawConn.(*net.TCPConn); ok {
		_ = tcpConn.SetLinger(0)
	}
}

func handleUDPResponse(s *ServerUDPSession, link *transport.Link, fallbackDest net.Destination, encode func(dest net.Destination, payload []byte) ([]byte, error)) {
	defer s.Close()
	for {
		resMb, err := link.Reader.ReadMultiBuffer()
		if err != nil {
			buf.ReleaseMulti(resMb)
			return
		}
		s.timer.Update()
		for i, rb := range resMb {
			b := rb.Bytes()
			if encode != nil {
				replyDest := fallbackDest
				if rb.UDP != nil {
					replyDest = *rb.UDP
				}
				encPacket, err := encode(replyDest, b)
				rb.Release()
				if err != nil {
					continue
				}
				if err := s.WriteToClient(encPacket); err != nil {
					buf.ReleaseMulti(resMb[i+1:])
					return
				}
			} else {
				err := s.WriteToClient(b)
				rb.Release()
				if err != nil {
					buf.ReleaseMulti(resMb[i+1:])
					return
				}
			}
		}
	}
}

const (
	HeaderTypeClient              = 0
	HeaderTypeServer              = 1
	MaxPaddingLength              = 900
	PacketNonceSize               = 24
	MaxPacketSize                 = 65535
	RequestHeaderFixedChunkLength = 1 + 8 + 2 // Type (1B) + Timestamp (8B) + VarHeaderLen (2B)
	PacketMinimalHeaderSize       = 30
	StreamNonceSize               = 12
	AESBlockSize                  = 16
	AEADTagSize                   = 16
)

var zeroPadding [MaxPaddingLength]byte

const (
	MethodAES128GCM        = "2022-blake3-aes-128-gcm"
	MethodAES256GCM        = "2022-blake3-aes-256-gcm"
	MethodChaCha20Poly1305 = "2022-blake3-chacha20-poly1305"
)

var (
	ErrBadKey            = errors.New("bad key")
	ErrBadHeaderType     = errors.New("bad header type")
	ErrBadTimestamp      = errors.New("bad timestamp")
	ErrSaltNotUnique     = errors.New("salt not unique")
	ErrPacketIdNotUnique = errors.New("packet id not unique")
	ErrPacketTooShort    = errors.New("packet too short")
	ErrPacketTooLarge    = errors.New("packet too large")
	ErrNoPadding         = errors.New("bad request: missing payload or padding")
	ErrInvalidRequest    = errors.New("invalid request")
)
