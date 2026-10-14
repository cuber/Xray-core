package shadowsocks_2022

import (
	"context"
	"crypto/cipher"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/utils"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
)

const (
	swBlockBitLog = 6                                // 1<<6 == 64 bits
	swBlockBits   = 1 << swBlockBitLog               // 64
	swRingBlocks  = 1 << 7                           // 128
	swBlockMask   = swRingBlocks - 1                 // 127
	swBitMask     = swBlockBits - 1                  // 63
	swSize        = (swRingBlocks - 1) * swBlockBits // 8128
)

type SlidingWindow struct {
	last uint64
	ring [swRingBlocks]uint64
}

func (f *SlidingWindow) Reset() {
	*f = SlidingWindow{}
}

func (f *SlidingWindow) Check(counter uint64) bool {
	switch {
	case counter > f.last:
		return true
	case f.last-counter > swSize:
		return false
	}

	blockIndex := (counter >> swBlockBitLog) & swBlockMask
	bitIndex := counter & swBitMask
	return (f.ring[blockIndex]>>bitIndex)&1 == 0
}

func (f *SlidingWindow) Add(counter uint64) {
	blockIndex := counter >> swBlockBitLog

	if counter > f.last {
		lastBlockIndex := f.last >> swBlockBitLog
		diff := int(blockIndex - lastBlockIndex)
		if diff > swRingBlocks {
			diff = swRingBlocks
		}

		for i := 0; i < diff; i++ {
			lastBlockIndex = (lastBlockIndex + 1) & swBlockMask
			f.ring[lastBlockIndex] = 0
		}

		f.last = counter
	}

	blockIndex &= swBlockMask
	bitIndex := counter & swBitMask
	f.ring[blockIndex] |= 1 << bitIndex
}

func (f *SlidingWindow) CheckAndAdd(counter uint64) bool {
	if !f.Check(counter) {
		return false
	}
	f.Add(counter)
	return true
}

type ServerUDPSession struct {
	sync.Mutex
	SessionID   uint64
	Window      *SlidingWindow
	User        *protocol.MemoryUser
	UserPSK     []byte
	relayTarget *relayDest
	LastActive  atomic.Int64 // Unix timestamp in seconds

	clientBodyCipher cipher.AEAD

	ServerSessionID   uint64
	ServerPacketID    atomic.Uint64
	serverBodyCipher  cipher.AEAD
	serverHeaderBlock cipher.Block
	serverChaCha      cipher.AEAD

	manager      *UDPSessionManager
	link         atomic.Pointer[transport.Link]
	timer        *signal.ActivityTimer
	currentConn  atomic.Pointer[udpClientConn]
	closed       bool
	expiry       *time.Timer
	cancel       context.CancelFunc
	done         chan struct{}
	initializing chan struct{}
}

type udpClientConn struct{ conn stat.Connection }

func (s *ServerUDPSession) touch(timeout time.Duration) {
	s.Lock()
	defer s.Unlock()
	if s.closed {
		return
	}
	s.LastActive.Store(time.Now().Unix())
	if timeout <= 0 {
		timeout = time.Minute
	}
	if s.expiry == nil {
		s.expiry = time.AfterFunc(timeout, s.Close)
	} else {
		s.expiry.Reset(timeout)
	}
}

func (s *ServerUDPSession) CheckPacketID(packetID uint64) bool {
	s.Lock()
	defer s.Unlock()
	if s.Window == nil {
		s.Window = new(SlidingWindow)
	}
	return s.Window.Check(packetID)
}

func (s *ServerUDPSession) AddPacketID(packetID uint64) {
	s.Lock()
	defer s.Unlock()
	if s.Window == nil {
		s.Window = new(SlidingWindow)
	}
	s.Window.Add(packetID)
}

type UDPSessionManager struct {
	sync.Mutex
	sessions *utils.TypedSyncMap[uint64, *ServerUDPSession]
	timeout  time.Duration
	closed   bool
}

func NewUDPSessionManager(timeout time.Duration) *UDPSessionManager {
	return &UDPSessionManager{
		sessions: utils.NewTypedSyncMap[uint64, *ServerUDPSession](),
		timeout:  timeout,
	}
}

func (m *UDPSessionManager) GetOrCreate(sessionID uint64) *ServerUDPSession {
	m.Lock()
	defer m.Unlock()
	if m.closed {
		return nil
	}
	if s, ok := m.sessions.Load(sessionID); ok {
		s.touch(m.timeout)
		return s
	}

	s := &ServerUDPSession{
		SessionID: sessionID,
		manager:   m,
	}
	m.sessions.Store(sessionID, s)
	s.touch(m.timeout)

	return s
}

func (m *UDPSessionManager) Close() error {
	m.Lock()
	defer m.Unlock()
	m.closed = true
	m.sessions.Range(func(_ uint64, s *ServerUDPSession) bool { s.Close(); return true })
	return nil
}

func (m *UDPSessionManager) cleanup(now int64) {
	timeoutSec := int64(m.timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	m.sessions.Range(func(k uint64, v *ServerUDPSession) bool {
		if now-v.LastActive.Load() > timeoutSec {
			v.Close()
		}
		return true
	})
}

func (m *UDPSessionManager) Delete(sessionID uint64) {
	if s, ok := m.sessions.Load(sessionID); ok {
		s.Close()
	}
}

func (m *UDPSessionManager) EncodeServerPacket(method *CipherMethod, psk []byte, clientSessionID uint64, dest net.Destination, payload []byte) ([]byte, error) {
	sessionItem, ok := m.sessions.Load(clientSessionID)
	if !ok {
		return nil, ErrInvalidRequest
	}
	if err := sessionItem.EnsureServerState(method, psk); err != nil {
		return nil, err
	}
	return sessionItem.EncodeServerPacket(method, clientSessionID, dest, payload)
}

// Authenticate publishes replay/cipher state only after authentication succeeds.
// Serializing publication also makes replay check-and-add atomic across transports.
func (m *UDPSessionManager) authenticate(id uint64, user *protocol.MemoryUser, decode func(*ServerUDPSession) (DecodedUDPPacket, error)) (DecodedUDPPacket, *ServerUDPSession, error) {
	m.Lock()
	defer m.Unlock()
	if m.closed {
		return DecodedUDPPacket{}, nil, ErrInvalidRequest
	}
	s, exists := m.sessions.Load(id)
	if !exists {
		s = &ServerUDPSession{SessionID: id, manager: m, User: user}
	}
	s.Lock()
	rejected := s.closed || (user != nil && s.User != user)
	s.Unlock()
	if rejected {
		return DecodedUDPPacket{}, nil, ErrInvalidRequest
	}
	p, err := decode(s)
	if err != nil {
		return DecodedUDPPacket{}, nil, err
	}
	if !exists {
		m.sessions.Store(id, s)
	}
	s.touch(m.timeout)
	return p, s, nil
}
