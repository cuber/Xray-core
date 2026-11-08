package hysteria

import (
	"context"
	gotls "crypto/tls"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/finalmask"
	"github.com/xtls/xray-core/transport/internet/hysteria/congestion"
	"github.com/xtls/xray-core/transport/internet/hysteria/congestion/bbr"
	"github.com/xtls/xray-core/transport/internet/quicrecovery"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/internet/tls"
)

type client struct {
	lock         chan struct{}
	instance     *core.Instance
	forced       bool
	ownerContext context.Context
	ownerCancel  context.CancelFunc
	dest         net.Destination
	config       *Config
	tlsConfig    *gotls.Config
	socketConfig *internet.SocketConfig
	finalMask    *finalmask.FinalMask
	quicParams   *internet.QuicParams
	quicStats    *internet.QuicStatsSet // the outbound's QUIC connections (spec021 R3)

	conn    *quic.Conn
	tr      *quic.Transport
	pktConn net.PacketConn
	udpSM   *udpSessionManager

	// The relay negotiation of the current connection (spec024 R2) and its UDP sessions, guarded by
	// relayMu so that status reports never wait for a dial.
	relayMu       sync.Mutex
	relayAnswered bool
	relayBindings map[uint32]RelayBinding
	liveUDP       *udpSessionManager

	// live is the current connection for readers outside the lock (a pool's placement and reports).
	live atomic.Pointer[quic.Conn]
	// onConnect runs after each successful dial (a pool member registers its row detail).
	onConnect func(internet.QuicConn)
}

// liveStatus is status without the lock.
func (c *client) liveStatus() status {
	conn := c.live.Load()
	if conn == nil {
		return StatusNull
	}
	select {
	case <-conn.Context().Done():
		return StatusInactive
	default:
		return StatusActive
	}
}

func (c *client) status() status {
	if c.conn == nil {
		return StatusNull
	}
	select {
	case <-c.conn.Context().Done():
		return StatusInactive
	default:
		return StatusActive
	}
}

func (c *client) close() {
	c.live.Store(nil)
	c.conn.CloseWithError(closeErrCodeOK, "")
	c.quicStats.Remove(quicrecovery.Conn(c.conn))
	// Dispatch-backed PacketConn deadlines are no-ops. Unblock its reader
	// before Transport.Close joins QUIC's listener goroutine.
	c.pktConn.Close()
	c.tr.Close()
	c.conn = nil
	c.tr = nil
	c.pktConn = nil
	c.udpSM = nil
	c.relayMu.Lock()
	c.relayAnswered, c.relayBindings, c.liveUDP = false, nil, nil
	c.relayMu.Unlock()
}

func (c *client) dial(ctx context.Context) error {
	if c.forced {
		return errors.New("client is closed")
	}

	switch c.status() {
	case StatusActive:
		return nil
	case StatusInactive:
		c.close()
	}

	quicParams := c.quicParams
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
		KeepAlivePeriod:                time.Duration(quicParams.KeepAlivePeriod) * time.Second,
		DisablePathMTUDiscovery:        quicParams.DisablePathMtuDiscovery || (runtime.GOOS != "linux" && runtime.GOOS != "windows" && runtime.GOOS != "darwin"),
		ChromeParrot:                   !quicParams.DisableChromeParrot,
		EnableDatagrams:                true,
		MaxDatagramFrameSize:           MaxDatagramFrameSize,
		OmitMaxDatagramFrameSize:       true,
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
	// if quicParams.KeepAlivePeriod == 0 {
	// 	quicConfig.KeepAlivePeriod = 10 * time.Second
	// }

	var pktConn net.PacketConn
	var udpAddr net.Addr
	if c.finalMask != nil {
		conn, err := c.finalMask.DialUDP(ctx, c.dest)
		if err != nil {
			return errors.New("failed to dial to dest").Base(err)
		}
		pktConn = conn.(*net.PacketConnWrapper).PacketConn
		udpAddr = conn.RemoteAddr()
	} else {
		conn, err := internet.DialSystem(c.systemDialContext(ctx), c.dest, c.socketConfig)
		if err != nil {
			return errors.New("failed to dial to dest").Base(err)
		}
		switch c := conn.(type) {
		case *net.PacketConnWrapper:
			pktConn = c.PacketConn
			udpAddr = c.RemoteAddr()
		case *cnc.Connection:
			pktConn = &internet.FakePacketConn{Conn: c}
			udpAddr = &net.UDPAddr{IP: []byte{0, 0, 0, 0}}
		default:
			_ = conn.Close()
			return errors.New("unsupported packet connection: ", reflect.TypeOf(c))
		}
	}

	tr := &quic.Transport{Conn: pktConn, DisableGSO: quicParams.DisableGSO}

	if !quicParams.DisableChromeParrot {
		tr.ConnectionIDGenerator = quic.ZeroLengthConnectionIDGenerator{}
		c.tlsConfig.GetCertificate = nil
	}

	var conn *quic.Conn
	rt := &http3.Transport{
		TLSClientConfig: c.tlsConfig,
		QUICConfig:      quicConfig,
		Dial: func(ctx context.Context, _ string, tlsCfg *gotls.Config, cfg *quic.Config) (*quic.Conn, error) {
			qc, err := tr.DialEarly(ctx, udpAddr, tlsCfg, cfg)
			if err != nil {
				return nil, err
			}
			conn = qc
			return qc, nil
		},
	}
	req := &http.Request{
		Method: http.MethodPost,
		URL: &url.URL{
			Scheme: "https",
			Host:   URLHost,
			Path:   URLPath,
		},
		Header: http.Header{
			RequestHeaderAuth: []string{c.config.Auth},
			CommonHeaderCCRX:  []string{strconv.FormatUint(quicParams.BrutalDown, 10)},
		},
	}
	setPadding(req.Header, AuthRequestPadding, c.config)
	relays := c.config.GetSession().GetRelayAuth()
	if len(relays) > 0 {
		req.Header.Set(HeaderRelay, encodeRelayRequest(relays))
	}
	resp, err := rt.RoundTrip(req.WithContext(ctx))
	if err != nil {
		if conn != nil {
			_ = conn.CloseWithError(closeErrCodeProtocolError, "")
		}
		_ = pktConn.Close()
		_ = tr.Close()
		_ = rt.Close()
		return err
	}
	_ = resp.Body.Close()

	if resp.StatusCode != StatusAuthOK {
		_ = conn.CloseWithError(closeErrCodeProtocolError, "")
		_ = pktConn.Close()
		_ = tr.Close()
		_ = rt.Close()
		return errors.New("auth failed code ", resp.StatusCode)
	}

	// udp, _ := strconv.ParseBool(resp.Header.Get(ResponseHeaderUDPEnabled))
	down, _ := strconv.ParseUint(resp.Header.Get(CommonHeaderCCRX), 10, 64)
	errors.LogDebug(context.Background(), "ECHAccepted ", conn.ConnectionState().TLS.ECHAccepted)

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

	answered, bindings := false, map[uint32]RelayBinding(nil)
	if len(relays) > 0 {
		if v := resp.Header.Get(HeaderRelay); v != "" {
			if answers, err := decodeRelayResponse(v); err != nil {
				errors.LogWarning(ctx, "hysteria relay answer unreadable; no relaying on this connection: ", err)
			} else {
				answered, bindings = true, answers
			}
		}
	}

	c.pktConn = pktConn
	c.tr = tr
	c.conn = conn
	c.relayMu.Lock()
	c.relayAnswered, c.relayBindings = answered, bindings
	c.relayMu.Unlock()
	c.quicStats.Add(quicrecovery.Conn(conn))
	c.udpSM = &udpSessionManager{
		conn: conn,
		m:    make(map[uint32]*InterConn),
		next: 1,

		relayed: answered,
	}
	go c.udpSM.run()
	c.relayMu.Lock()
	c.liveUDP = c.udpSM
	c.relayMu.Unlock()
	c.live.Store(conn)
	if c.onConnect != nil {
		c.onConnect(quicrecovery.Conn(conn))
	}

	return nil
}

func (c *client) systemDialContext(ctx context.Context) context.Context {
	if !c.dest.Address.Family().IsIP() || !c.dest.Address.IP().IsLoopback() ||
		(c.socketConfig != nil && c.socketConfig.DialerProxy != "") {
		return ctx
	}
	outbounds := session.OutboundsFromContext(ctx)
	var ob session.Outbound
	if len(outbounds) > 0 {
		ob = *outbounds[len(outbounds)-1]
		if ob.Gateway != nil {
			return ctx
		}
	}
	// Darwin can allocate a wildcard UDP ephemeral port already used by a
	// specific loopback listener; replies then go to that listener. Bind the
	// loopback source explicitly so the allocator checks the same address.
	ob.Gateway = net.LocalHostIP
	if c.dest.Address.Family().IsIPv6() {
		ob.Gateway = net.LocalHostIPv6
	}
	owned := append([]*session.Outbound(nil), outbounds...)
	if len(owned) == 0 {
		owned = append(owned, &ob)
	} else {
		owned[len(owned)-1] = &ob
	}
	return session.ContextWithOutbounds(ctx, owned)
}

func (c *client) unlock() {
	<-c.lock
}

func (c *client) tcp(ctx context.Context) (stat.Connection, error) {
	return c.tcpOpen(ctx, false)
}

// tcpOpen opens a stream, dialing first when needed; with wait, it waits for stream credit (a pool whose
// connections all reached the peer's stream limit, spec024 R4).
func (c *client) tcpOpen(ctx context.Context, wait bool) (*interConn, error) {
	ctx, cancel := c.dialContext(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c.lock <- struct{}{}:
	}
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			c.unlock()
		}
	}
	defer unlock()

	err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	index := RelayIndexFromContext(ctx)
	if err := c.relayReady(index); err != nil {
		return nil, err
	}

	conn := c.conn
	var stream *quic.Stream
	if wait {
		unlock() // the wait must not hold the dial lock
		stream, err = conn.OpenStreamSync(ctx)
	} else {
		stream, err = conn.OpenStream()
	}
	if err != nil {
		return nil, err
	}

	return &interConn{
		stream: stream,
		local:  conn.LocalAddr(),
		remote: conn.RemoteAddr(),

		client:     true,
		relayIndex: index,
		// Every client stream is opened here, on the client of the connection that carries it: a plain
		// outbound's, a pool member's, and a relay outbound's on its owner's (spec025 R3).
		noPadding: c.config.GetDisablePadding(),
	}, nil
}

// connect dials the connection in the background (a pool member), under the client's owner context.
func (c *client) connect() error {
	ctx, cancel := c.dialContext(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.lock <- struct{}{}:
		defer c.unlock()
	}
	return c.dial(ctx)
}

// udpSessions is the number of live UDP sessions on the current connection.
func (c *client) udpSessions() int {
	c.relayMu.Lock()
	m := c.liveUDP
	c.relayMu.Unlock()
	if m == nil {
		return 0
	}
	m.RLock()
	defer m.RUnlock()
	return len(m.m)
}

// relayReport is the current connection's bindings for its report row (spec024 R7).
func (c *client) relayReport() []internet.QuicRelayBinding {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if len(c.relayBindings) == 0 {
		return nil
	}
	out := make([]internet.QuicRelayBinding, 0, len(c.relayBindings))
	for index := uint32(1); index <= MaxRelayIndex && len(out) < len(c.relayBindings); index++ {
		if b, ok := c.relayBindings[index]; ok {
			out = append(out, internet.QuicRelayBinding{Index: index, User: b.User, Status: b.Status})
		}
	}
	return out
}

// relayReady refuses a relayed stream the current connection cannot carry (spec024 R2).
func (c *client) relayReady(index uint32) error {
	if index == 0 {
		return nil
	}
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if !c.relayAnswered {
		return errors.New("hysteria relay: the server does not relay (no ", HeaderRelay, " answer)")
	}
	if binding := c.relayBindings[index]; binding.Status != RelayStatusOK {
		return errors.New("hysteria relay: the server refused relay index ", index, ": ", binding.Status)
	}
	return nil
}

// Relay outbound states as reported (spec024 R6, R7).
const (
	RelayBound        = "bound"
	RelayUnsupported  = "unsupported"
	RelayNoConnection = "no_connection"
)

func (c *client) relayStatus(index uint32) (status, user string) {
	if c.liveStatus() != StatusActive {
		return RelayNoConnection, ""
	}
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if !c.relayAnswered {
		return RelayUnsupported, ""
	}
	binding, ok := c.relayBindings[index]
	switch {
	case !ok:
		return RelayUnsupported, ""
	case binding.Status == RelayStatusOK:
		return RelayBound, binding.User
	default:
		return binding.Status, ""
	}
}

// RelayStatus reports a relay outbound's binding on its owner's connection, without dialing (spec024 R6,
// R7): the owner is found by the key Dial uses.
func RelayStatus(instance *core.Instance, dest net.Destination, streamSettings *internet.MemoryStreamConfig, index uint32) (status, user string) {
	if manager == nil {
		return RelayNoConnection, ""
	}
	dest.Network = net.Network_UDP
	key := dialerConf{Destination: dest, MemoryStreamConfig: streamSettings, owner: instance}
	manager.RLock()
	c, p := manager.m[key], manager.pools[key]
	manager.RUnlock()
	switch {
	case p != nil:
		return p.relayStatus(index)
	case c != nil:
		return c.relayStatus(index)
	}
	return RelayNoConnection, ""
}

func (c *client) udp(ctx context.Context) (stat.Connection, error) {
	ctx, cancel := c.dialContext(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c.lock <- struct{}{}:
		defer c.unlock()
	}

	err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	index := RelayIndexFromContext(ctx)
	if err := c.relayReady(index); err != nil {
		return nil, err
	}

	return c.udpSM.udp(index)
}

func (c *client) clean(force bool) {
	if force && c.ownerCancel != nil {
		c.ownerCancel()
	}
	c.lock <- struct{}{}
	if force {
		c.forced = true
	}
	if status := c.status(); force && status != StatusNull || status == StatusInactive {
		c.close()
	}
	c.unlock()
}

func (c *client) dialContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if c.ownerContext == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(c.ownerContext, cancel)
	return ctx, func() { stop(); cancel() }
}

type dialerConf struct {
	net.Destination
	*internet.MemoryStreamConfig
	owner *core.Instance
}

type clientManager struct {
	sync.RWMutex
	m     map[dialerConf]*client
	pools map[dialerConf]*pool // session owners (spec024 R4)
}

func (m *clientManager) clean() {
	ticker := time.NewTicker(idleCleanupInterval)
	for range ticker.C {
		m.cleanOnce()
	}
}

func (m *clientManager) cleanOnce() {
	m.RLock()
	entries := make(map[dialerConf]*client, len(m.m))
	for k, c := range m.m {
		entries[k] = c
	}
	pools := make(map[dialerConf]*pool, len(m.pools))
	for k, p := range m.pools {
		pools[k] = p
	}
	m.RUnlock()
	for k, c := range entries {
		force := c.instance != nil && !c.instance.IsRunning()
		c.clean(force)
		if force {
			m.Lock()
			if m.m[k] == c {
				delete(m.m, k)
			}
			m.Unlock()
		}
	}
	for k, p := range pools {
		force := k.owner != nil && !k.owner.IsRunning()
		p.clean(force)
		if force {
			m.Lock()
			if m.pools[k] == p {
				delete(m.pools, k)
			}
			m.Unlock()
		}
	}
}

var (
	manager     *clientManager
	initmanager sync.Once
)

func Dial(ctx context.Context, dest net.Destination, streamSettings *internet.MemoryStreamConfig) (stat.Connection, error) {
	tlsConfig := tls.ConfigFromStreamSettings(streamSettings)
	if tlsConfig == nil {
		return nil, errors.New("tls config is nil")
	}

	datagram := DatagramFromContext(ctx)
	dest.Network = net.Network_UDP

	initmanager.Do(func() {
		manager = &clientManager{
			m:     make(map[dialerConf]*client),
			pools: make(map[dialerConf]*pool),
		}
		go manager.clean()
	})

	dialerConfKey := dialerConf{Destination: dest, MemoryStreamConfig: streamSettings, owner: core.FromContext(ctx)}
	if session := streamSettings.ProtocolSettings.(*Config).GetSession(); session != nil {
		p, err := sessionPool(ctx, dialerConfKey, session, tlsConfig)
		if err != nil {
			return nil, err
		}
		return p.open(ctx, datagram)
	}

	manager.RLock()
	c := manager.m[dialerConfKey]
	manager.RUnlock()

	if c == nil {
		manager.Lock()
		c = manager.m[dialerConfKey]
		if c == nil {
			c = &client{
				lock:         make(chan struct{}, 1),
				instance:     core.FromContext(ctx),
				dest:         dest,
				config:       streamSettings.ProtocolSettings.(*Config),
				tlsConfig:    tlsConfig.GetTLSConfig(tls.WithDestination(dest)),
				socketConfig: streamSettings.SocketSettings,
				finalMask:    streamSettings.FinalMask,
				quicParams:   streamSettings.QuicParams,
				quicStats:    streamSettings.QuicStats,
			}
			c.ownerContext, c.ownerCancel = context.WithCancel(context.Background())
			owned := c
			if c.instance != nil && !c.instance.OnClose(func() {
				manager.Lock()
				if manager.m[dialerConfKey] == owned {
					delete(manager.m, dialerConfKey)
				}
				manager.Unlock()
				owned.clean(true)
			}) {
				manager.Unlock()
				c.ownerCancel()
				return nil, errors.New("instance is closing")
			}
			manager.m[dialerConfKey] = c
		}
		manager.Unlock()
	}

	if datagram {
		return c.udp(ctx)
	}
	return c.tcp(ctx)
}

// sessionPool returns the owner's pool, creating it on the first dial (spec024 R4). Its members are
// clients like Dial's, sharing one owner context that the instance's close cancels.
func sessionPool(ctx context.Context, key dialerConf, session *Session, tlsConfig *tls.Config) (*pool, error) {
	manager.RLock()
	p := manager.pools[key]
	manager.RUnlock()
	if p != nil {
		return p, nil
	}
	manager.Lock()
	defer manager.Unlock()
	if p = manager.pools[key]; p != nil {
		return p, nil
	}
	instance := core.FromContext(ctx)
	ownerContext, ownerCancel := context.WithCancel(context.Background())
	streamSettings := key.MemoryStreamConfig
	p = newPool(session, streamSettings.QuicStats, func() *client {
		return &client{
			lock:         make(chan struct{}, 1),
			instance:     instance,
			ownerContext: ownerContext,
			dest:         key.Destination,
			config:       streamSettings.ProtocolSettings.(*Config),
			tlsConfig:    tlsConfig.GetTLSConfig(tls.WithDestination(key.Destination)),
			socketConfig: streamSettings.SocketSettings,
			finalMask:    streamSettings.FinalMask,
			quicParams:   streamSettings.QuicParams,
			quicStats:    streamSettings.QuicStats,
		}
	})
	owned := p
	if instance != nil && !instance.OnClose(func() {
		ownerCancel()
		manager.Lock()
		if manager.pools[key] == owned {
			delete(manager.pools, key)
		}
		manager.Unlock()
		owned.clean(true)
	}) {
		ownerCancel()
		return nil, errors.New("instance is closing")
	}
	manager.pools[key] = p
	return p, nil
}

func init() {
	common.Must(internet.RegisterTransportDialer(protocolName, Dial))
}
