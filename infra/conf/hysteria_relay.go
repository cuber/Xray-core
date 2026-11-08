package conf

import (
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/hysteria"

	hysteriaproxy "github.com/xtls/xray-core/proxy/hysteria"
)

// maxHysteriaRelays is the index space of a session (spec024 R2): 1–255, 0 being the connection user.
const maxHysteriaRelays = 255

// hysteriaTransport returns the stream's Hy2 transport settings, or nil.
func hysteriaTransport(ss *internet.StreamConfig) *hysteria.Config {
	if ss == nil {
		return nil
	}
	for _, ts := range ss.TransportSettings {
		if ts.ProtocolName != "hysteria" || ts.Settings == nil {
			continue
		}
		if instance, err := ts.Settings.GetInstance(); err == nil {
			if config, ok := instance.(*hysteria.Config); ok {
				return config
			}
		}
	}
	return nil
}

// checkHysteriaSessionKeepAlive enforces spec024 R1: a pool keeps its idle and spare connections open with
// keepalives shorter than the idle timeout, never through an implicit default.
func checkHysteriaSessionKeepAlive(q *internet.QuicParams) error {
	idle := int64(30)
	if q != nil && q.MaxIdleTimeout != 0 {
		idle = q.MaxIdleTimeout
	}
	if q == nil || q.KeepAlivePeriod == 0 || q.KeepAlivePeriod >= idle {
		return errors.New("hysteriaSettings.session needs finalmask.quicParams.keepAlivePeriod shorter than the idle timeout (",
			idle, " s): a pool keeps its idle connections open with keepalives")
	}
	return nil
}

type hysteriaOwner struct {
	handler   *core.OutboundHandlerConfig
	sender    *proxyman.SenderConfig
	transport *internet.TransportConfig
	settings  *hysteria.Config
}

// linkHysteriaRelays checks the relay outbounds against their session owners and numbers them (spec024 R1,
// R2): each relay outbound gets its 1-based position in its session as its index, and the owner's session
// lists the relay credentials in that order for the negotiation.
func linkHysteriaRelays(outbounds []*core.OutboundHandlerConfig) error {
	owners := make(map[string]*hysteriaOwner)
	relays := make(map[string]*hysteriaproxy.ClientConfig)
	for _, oc := range outbounds {
		client := hysteriaClientOf(oc)
		if client == nil {
			continue
		}
		if client.Relay != nil {
			relays[oc.Tag] = client
			continue
		}
		owner, err := hysteriaOwnerOf(oc)
		if err != nil {
			return err
		}
		if owner != nil {
			owners[oc.Tag] = owner
		}
	}
	if len(relays) == 0 {
		return nil
	}

	credentials := make(map[string]map[string]string) // owner tag -> credential -> relay tag
	for _, oc := range outbounds {
		client := relays[oc.Tag]
		if client == nil {
			continue
		}
		relay := client.Relay
		if oc.SenderSettings != nil {
			if sender, err := oc.SenderSettings.GetInstance(); err == nil && sender.(*proxyman.SenderConfig).StreamSettings != nil {
				return errors.New("hysteria relay outbound ", oc.Tag, " has streamSettings: it sends through its session owner ", relay.Session)
			}
		}
		owner := owners[relay.Session]
		if owner == nil {
			if relays[relay.Session] != nil {
				return errors.New("hysteria relay outbound ", oc.Tag, " names relay outbound ", relay.Session, " as its session: relays do not chain")
			}
			return errors.New("hysteria relay outbound ", oc.Tag, " names ", relay.Session, ", which is not a Hy2 outbound with hysteriaSettings.session")
		}
		if relay.Auth == owner.settings.Auth {
			return errors.New("hysteria relay outbound ", oc.Tag, " carries its session owner's credential")
		}
		seen := credentials[relay.Session]
		if seen == nil {
			seen = make(map[string]string)
			credentials[relay.Session] = seen
		}
		if other, ok := seen[relay.Auth]; ok {
			return errors.New("hysteria relay outbounds ", other, " and ", oc.Tag, " carry the same credential")
		}
		if len(seen) == maxHysteriaRelays {
			return errors.New("hysteria session ", relay.Session, " has more than ", maxHysteriaRelays, " relay outbounds")
		}
		seen[relay.Auth] = oc.Tag
		owner.settings.Session.RelayAuth = append(owner.settings.Session.RelayAuth, relay.Auth)
		relay.Index = uint32(len(owner.settings.Session.RelayAuth))
		oc.ProxySettings = serial.ToTypedMessage(client)
	}
	for tag, owner := range owners {
		if len(credentials[tag]) == 0 {
			continue
		}
		owner.transport.Settings = serial.ToTypedMessage(owner.settings)
		owner.handler.SenderSettings = serial.ToTypedMessage(owner.sender)
	}
	return nil
}

func hysteriaClientOf(oc *core.OutboundHandlerConfig) *hysteriaproxy.ClientConfig {
	if oc.ProxySettings == nil {
		return nil
	}
	instance, err := oc.ProxySettings.GetInstance()
	if err != nil {
		return nil
	}
	client, _ := instance.(*hysteriaproxy.ClientConfig)
	return client
}

func hysteriaOwnerOf(oc *core.OutboundHandlerConfig) (*hysteriaOwner, error) {
	if oc.SenderSettings == nil {
		return nil, nil
	}
	instance, err := oc.SenderSettings.GetInstance()
	if err != nil {
		return nil, errors.New("failed to read the sender settings of outbound ", oc.Tag).Base(err)
	}
	sender := instance.(*proxyman.SenderConfig)
	if sender.StreamSettings == nil {
		return nil, nil
	}
	for _, ts := range sender.StreamSettings.TransportSettings {
		if ts.ProtocolName != "hysteria" || ts.Settings == nil {
			continue
		}
		settings, err := ts.Settings.GetInstance()
		if err != nil {
			return nil, errors.New("failed to read the hysteria settings of outbound ", oc.Tag).Base(err)
		}
		config := settings.(*hysteria.Config)
		if config.Session == nil {
			return nil, nil
		}
		return &hysteriaOwner{handler: oc, sender: sender, transport: ts, settings: config}, nil
	}
	return nil, nil
}
