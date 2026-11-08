package hysteria

import (
	"context"
	"crypto/rand"
	gotls "crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/hysteria/congestion"
	"github.com/xtls/xray-core/transport/internet/hysteria/congestion/bbr"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"github.com/xtls/xray-core/transport/internet/tls"
)

type httpHandler struct {
	sync.Mutex

	validator   *account.Validator
	config      *Config
	masqHandler http.Handler
	quicParams  *internet.QuicParams
	quicStats   *internet.QuicStatsSet
	addConn     internet.ConnHandler
	conn        *quic.Conn

	auth bool
	user *protocol.MemoryUser

	// The relayed users this connection negotiated (spec024 R2, R3), by index, and the answer to repeat.
	relays      map[uint32]string
	relayAnswer string
}

func (h *httpHandler) AuthHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost && r.Host == URLHost && r.URL.Path == URLPath {
		h.Lock()
		defer h.Unlock()

		if h.auth {
			if h.relayAnswer != "" {
				w.Header().Set(HeaderRelay, h.relayAnswer)
			}
			w.Header().Set(ResponseHeaderUDPEnabled, strconv.FormatBool(h.validator != nil))
			w.Header().Set(CommonHeaderCCRX, strconv.FormatUint(h.quicParams.BrutalDown, 10))
			setPadding(w.Header(), AuthResponsePadding, h.config)
			w.WriteHeader(StatusAuthOK)
			return true
		}

		auth := r.Header.Get(RequestHeaderAuth)
		down, _ := strconv.ParseUint(r.Header.Get(CommonHeaderCCRX), 10, 64)

		var user *protocol.MemoryUser
		var ok bool
		if h.validator != nil && h.validator.NotEmpty() {
			user = h.validator.Get(auth)
		} else if h.config.Auth != "" {
			ok = auth == h.config.Auth
		}

		if user != nil || ok {
			h.auth = true
			h.user = user
			if user != nil {
				h.quicStats.SetUser(quicrecovery.Conn(h.conn), user.Email)
				h.negotiateRelays(user, r.Header.Get(HeaderRelay))
			}

			conn := h.conn
			quicParams := h.quicParams
			switch quicParams.Congestion {
			case "reno":
			case "bbr":
				congestion.UseBBR(conn, bbr.Profile(quicParams.BbrProfile))
			case "", "brutal":
				if quicParams.BrutalUp == 0 || down == 0 {
					congestion.UseBBR(conn, bbr.Profile(quicParams.BbrProfile))
				} else {
					congestion.UseBrutal(conn, min(quicParams.BrutalUp, down), quicParams.BrutalDisableLossCompensation)
				}
			case "force-brutal":
				congestion.UseBrutal(conn, quicParams.BrutalUp, quicParams.BrutalDisableLossCompensation)
			default:
				panic(quicParams.Congestion)
			}

			if h.validator != nil {
				udpSM := &udpSessionManager{
					conn: h.conn,
					m:    make(map[uint32]*InterConn),

					addConn:        h.addConn,
					udpIdleTimeout: time.Duration(h.config.UdpIdleTimeout) * time.Second,
					user:           h.user,
				}
				if len(h.relays) > 0 {
					udpSM.relayedUser = h.relayedUser
				}
				go udpSM.clean()
				go udpSM.run()
			}

			if h.relayAnswer != "" {
				w.Header().Set(HeaderRelay, h.relayAnswer)
			}
			w.Header().Set(ResponseHeaderUDPEnabled, strconv.FormatBool(h.validator != nil))
			w.Header().Set(CommonHeaderCCRX, strconv.FormatUint(h.quicParams.BrutalDown, 10))
			setPadding(w.Header(), AuthResponsePadding, h.config)
			w.WriteHeader(StatusAuthOK)
			return true
		}
	}
	return false
}

// negotiateRelays answers a relay list (spec024 R2): each credential must be a client of the inbound
// (authentication) on the connection user's relay list (authorization). Called under h's lock.
func (h *httpHandler) negotiateRelays(user *protocol.MemoryUser, request string) {
	if request == "" || h.validator == nil {
		return
	}
	auths, err := decodeRelayRequest(request)
	if err != nil {
		errors.LogWarning(context.Background(), "hysteria relay request from ", h.conn.RemoteAddr(), " unreadable; not relaying: ", err)
		return
	}
	acc, _ := user.Account.(*account.MemoryAccount)
	answers := make(map[uint32]RelayBinding, len(auths))
	h.relays = make(map[uint32]string, len(auths))
	for index, auth := range auths {
		relayed := h.validator.Get(auth)
		switch {
		case relayed == nil:
			answers[index] = RelayBinding{Status: RelayStatusUnknown}
		case acc == nil || !acc.MayRelay(relayed.Email):
			answers[index] = RelayBinding{Status: RelayStatusDenied}
		default:
			answers[index] = RelayBinding{Status: RelayStatusOK, User: relayed.Email}
			h.relays[index] = auth
		}
	}
	h.relayAnswer = encodeRelayResponse(answers)
	var accepted []internet.QuicRelayBinding
	for index := uint32(1); index <= MaxRelayIndex && len(accepted) < len(h.relays); index++ {
		if answer, ok := answers[index]; ok && answer.Status == RelayStatusOK {
			accepted = append(accepted, internet.QuicRelayBinding{Index: index, User: answer.User, Status: answer.Status})
		}
	}
	h.quicStats.SetDetail(quicrecovery.Conn(h.conn), func() internet.QuicConnDetail {
		return internet.QuicConnDetail{Relay: accepted}
	})
}

// relayedUser is the user a relayed stream or UDP session of this index carries, checked again against
// the inbound so that a user removed at runtime is refused at once (spec024 R3); nil when refused.
func (h *httpHandler) relayedUser(index uint32) *protocol.MemoryUser {
	h.Lock()
	auth, ok := h.relays[index]
	h.Unlock()
	if !ok {
		return nil
	}
	return h.validator.Get(auth)
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.AuthHTTP(w, r) {
		return
	}
	h.masqHandler.ServeHTTP(w, r)
}

func (h *httpHandler) StreamDispatcher(ft http3.FrameType, stream *quic.Stream, err error) (bool, error) {
	if err != nil || !h.auth {
		return false, nil
	}

	switch ft {
	case FrameTypeTCPRequest:
		if _, err := quicvarint.Read(quicvarint.NewReader(stream)); err != nil {
			return false, err
		}

		h.addConn(&interConn{
			stream: stream,
			local:  h.conn.LocalAddr(),
			remote: h.conn.RemoteAddr(),

			user:      h.user,
			noPadding: h.config.GetDisablePadding(), // the inbound's setting (spec025 R3)
		})
		return true, nil
	case FrameTypeTCPRelayRequest:
		r := quicvarint.NewReader(stream)
		if _, err := quicvarint.Read(r); err != nil {
			return false, err
		}
		index, err := quicvarint.Read(r)
		if err != nil {
			return false, err
		}
		user := h.relayedUser(uint32(index))
		if user == nil {
			errors.LogInfo(context.Background(), "hysteria relay: refused index ", index, " from ", h.conn.RemoteAddr())
			stream.CancelRead(0)
			stream.CancelWrite(0)
			return true, nil
		}
		h.addConn(&interConn{
			stream: stream,
			local:  h.conn.LocalAddr(),
			remote: h.conn.RemoteAddr(),

			user:      user,
			noPadding: h.config.GetDisablePadding(),
		})
		return true, nil
	default:
		return false, nil
	}
}

type Listener struct {
	mu          sync.Mutex
	closed      bool
	connections map[*quic.Conn]struct{}
	serving     sync.WaitGroup
	closeOnce   sync.Once
	closeErr    error
	acceptDone  chan struct{}
	validator   *account.Validator
	config      *Config
	masqHandler http.Handler
	quicParams  *internet.QuicParams
	quicStats   *internet.QuicStatsSet // the inbound's QUIC connections, across its listeners (spec021 R3)
	addConn     internet.ConnHandler

	pktConn  net.PacketConn
	tr       *quic.Transport
	listener *quic.Listener
}

func (l *Listener) handleClient(conn *quic.Conn) {
	l.quicStats.Add(quicrecovery.Conn(conn))
	defer func() {
		l.quicStats.Remove(quicrecovery.Conn(conn))
		l.mu.Lock()
		delete(l.connections, conn)
		l.mu.Unlock()
		l.serving.Done()
	}()
	handler := &httpHandler{
		validator:   l.validator,
		config:      l.config,
		masqHandler: l.masqHandler,
		quicParams:  l.quicParams,
		quicStats:   l.quicStats,
		addConn:     l.addConn,
		conn:        conn,
	}
	h3s := http3.Server{
		Handler:          handler,
		StreamDispatcher: handler.StreamDispatcher,
	}
	err := h3s.ServeQUICConn(conn)
	_ = conn.CloseWithError(closeErrCodeOK, "")
	errors.LogDebug(context.Background(), conn.RemoteAddr(), " ServeQUICConn exited with ", err)
}

func (l *Listener) keepAccepting() {
	if l.acceptDone != nil {
		defer close(l.acceptDone)
	}
	for {
		conn, err := l.listener.Accept(context.Background())
		if err != nil {
			if err != quic.ErrServerClosed {
				errors.LogErrorInner(context.Background(), err, "failed to serve hysteria")
			}
			break
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			conn.CloseWithError(closeErrCodeOK, "listener closed")
			continue
		}
		if l.connections == nil {
			l.connections = make(map[*quic.Conn]struct{})
		}
		l.connections[conn] = struct{}{}
		l.serving.Add(1)
		l.mu.Unlock()
		go l.handleClient(conn)
	}
}

func (l *Listener) Addr() net.Addr {
	return l.listener.Addr()
}

func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		connections := make([]*quic.Conn, 0, len(l.connections))
		for conn := range l.connections {
			connections = append(connections, conn)
		}
		l.mu.Unlock()
		listenErr := l.listener.Close()
		if l.acceptDone != nil {
			<-l.acceptDone
		}
		// Transport.Close aborts locally without notifying peers. Close each
		// owned QUIC connection while the packet transport can still send.
		for _, conn := range connections {
			conn.CloseWithError(closeErrCodeOK, "listener closed")
		}
		l.serving.Wait()
		l.closeErr = errors.Combine(listenErr, l.tr.Close(), l.pktConn.Close())
	})
	return l.closeErr
}

func Listen(ctx context.Context, address net.Address, port net.Port, streamSettings *internet.MemoryStreamConfig, handler internet.ConnHandler) (internet.Listener, error) {
	if address.Family().IsDomain() {
		return nil, errors.New("address is domain")
	}

	tlsConfig := tls.ConfigFromStreamSettings(streamSettings)
	if tlsConfig == nil {
		return nil, errors.New("tls config is nil")
	}

	validator := ValidatorFromContext(ctx)
	config := streamSettings.ProtocolSettings.(*Config)

	if validator == nil && config.Auth == "" {
		return nil, errors.New("validator is nil")
	}

	var masqHandler http.Handler
	switch strings.ToLower(config.MasqType) {
	case "", "404":
		masqHandler = http.NotFoundHandler()
	case "file":
		masqHandler = http.FileServer(http.Dir(config.MasqFile))
	case "proxy":
		u, err := url.Parse(config.MasqUrl)
		if err != nil {
			return nil, err
		}
		transport := http.DefaultTransport.(*http.Transport)
		switch u.Scheme {
		case "http", "https":
			if config.MasqUrlInsecure {
				transport = transport.Clone()
				if transport.TLSClientConfig == nil {
					transport.TLSClientConfig = &gotls.Config{}
				}
				transport.TLSClientConfig.InsecureSkipVerify = true
			}
		case "", "unix":
			path := u.Path
			u = &url.URL{Scheme: "http", Host: "localhost"}
			dialer := &net.Dialer{Timeout: 30 * time.Second}
			transport = transport.Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", path)
			}
			transport.MaxIdleConns = masqueradeProxyMaxIdleConnections
			transport.MaxIdleConnsPerHost = masqueradeProxyMaxIdleConnsPerHost
		default:
			return nil, errors.New("unknown scheme")
		}
		masqHandler = &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(u)
				if !config.MasqUrlRewriteHost {
					r.Out.Host = r.In.Host
				}
				if config.MasqUrlXForwarded {
					r.SetXForwarded()
				}
			},
			Transport:  transport,
			BufferPool: newMasqueradeProxyBufferPool(),
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				errors.LogErrorInner(context.Background(), err, "HTTP reverse proxy error")
				w.WriteHeader(http.StatusBadGateway)
			},
		}
	case "string":
		masqHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range config.MasqStringHeaders {
				w.Header().Set(k, v)
			}
			if config.MasqStringStatusCode != 0 {
				w.WriteHeader(int(config.MasqStringStatusCode))
			} else {
				w.WriteHeader(http.StatusOK)
			}
			_, _ = w.Write([]byte(config.MasqString))
		})
	default:
		return nil, errors.New("unknown masq type")
	}

	quicParams := streamSettings.QuicParams
	if quicParams == nil {
		quicParams = &internet.QuicParams{
			BbrProfile: string(bbr.ProfileStandard),
		}
	}

	quicConfig := &quic.Config{
		InitialStreamReceiveWindow:     quicParams.InitStreamReceiveWindow,
		MaxStreamReceiveWindow:         quicParams.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: quicParams.InitConnReceiveWindow,
		MaxConnectionReceiveWindow:     quicParams.MaxConnReceiveWindow,
		MaxIdleTimeout:                 time.Duration(quicParams.MaxIdleTimeout) * time.Second,
		MaxIncomingStreams:             quicParams.MaxIncomingStreams,
		DisablePathMTUDiscovery:        quicParams.DisablePathMtuDiscovery || (runtime.GOOS != "linux" && runtime.GOOS != "windows" && runtime.GOOS != "darwin"),
		EnableDatagrams:                true,
		MaxDatagramFrameSize:           MaxDatagramFrameSize,
		AssumePeerMaxDatagramFrameSize: MaxDatagramFrameSize,
		DisablePathManager:             true,
	}
	quicrecovery.Apply(quicConfig, quicParams)
	if quicParams.InitStreamReceiveWindow == 0 {
		quicConfig.InitialStreamReceiveWindow = 8388608
	}
	if quicParams.MaxStreamReceiveWindow == 0 {
		quicConfig.MaxStreamReceiveWindow = 8388608
	}
	if quicParams.InitConnReceiveWindow == 0 {
		quicConfig.InitialConnectionReceiveWindow = 8388608 * 5 / 2
	}
	if quicParams.MaxConnReceiveWindow == 0 {
		quicConfig.MaxConnectionReceiveWindow = 8388608 * 5 / 2
	}
	if quicParams.MaxIdleTimeout == 0 {
		quicConfig.MaxIdleTimeout = 30 * time.Second
	}
	if quicParams.MaxIncomingStreams == 0 {
		quicConfig.MaxIncomingStreams = 1024
	}

	var pktConn net.PacketConn
	var err error
	if streamSettings.FinalMask != nil {
		pktConn, err = streamSettings.FinalMask.ListenPacket(context.Background(), &net.UDPAddr{IP: address.IP(), Port: int(port)})
	} else {
		pktConn, err = internet.ListenSystemPacket(context.Background(), &net.UDPAddr{IP: address.IP(), Port: int(port)}, streamSettings.SocketSettings)
	}
	if err != nil {
		return nil, err
	}

	var k *quic.StatelessResetKey
	if !quicParams.DisableStatelessReset {
		k = &quic.StatelessResetKey{}
		common.Must2(rand.Read((*k)[:]))
	}

	tr := &quic.Transport{Conn: pktConn, DisableGSO: quicParams.DisableGSO, StatelessResetKey: k}

	listener, err := tr.Listen(tlsConfig.GetTLSConfig(tls.WithNextProto("h3")), quicConfig)
	if err != nil {
		_ = tr.Close()
		_ = pktConn.Close()
		return nil, err
	}

	l := &Listener{
		acceptDone:  make(chan struct{}),
		validator:   validator,
		config:      config,
		masqHandler: masqHandler,
		quicParams:  quicParams,
		quicStats:   streamSettings.QuicStats,
		addConn:     handler,

		pktConn:  pktConn,
		tr:       tr,
		listener: listener,
	}

	go l.keepAccepting()

	return l, nil
}

func init() {
	common.Must(internet.RegisterTransportListener(protocolName, Listen))
}

const (
	masqueradeProxyBufferSize          = 32 * 1024
	masqueradeProxyMaxIdleConnections  = 100
	masqueradeProxyMaxIdleConnsPerHost = 32
)

type masqueradeProxyBufferPool struct {
	pool sync.Pool
}

func newMasqueradeProxyBufferPool() *masqueradeProxyBufferPool {
	return &masqueradeProxyBufferPool{
		pool: sync.Pool{
			New: func() any {
				return make([]byte, masqueradeProxyBufferSize)
			},
		},
	}
}

func (p *masqueradeProxyBufferPool) Get() []byte {
	return p.pool.Get().([]byte)
}

func (p *masqueradeProxyBufferPool) Put(buf []byte) {
	if cap(buf) < masqueradeProxyBufferSize {
		return
	}
	p.pool.Put(buf[:masqueradeProxyBufferSize])
}
