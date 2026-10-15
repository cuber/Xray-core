package command

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/transport/internet"
	tlsconfig "github.com/xtls/xray-core/transport/internet/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type controlInbound struct {
	inbound.Handler
	proxy.Inbound
	proxy.UserManager
	receiver *proxyman.ReceiverConfig
	user     *protocol.MemoryUser
}

func (h *controlInbound) Tag() string { return "entry" }
func (h *controlInbound) ReceiverSettings() *serial.TypedMessage {
	return serial.ToTypedMessage(h.receiver)
}

func (h *controlInbound) ProxySettings() *serial.TypedMessage {
	return serial.ToTypedMessage(h.user.Account.ToProto())
}
func (h *controlInbound) GetInbound() proxy.Inbound { return h }
func (h *controlInbound) GetUsers(context.Context) []*protocol.MemoryUser {
	return []*protocol.MemoryUser{h.user}
}

func (h *controlInbound) GetUser(_ context.Context, email string) *protocol.MemoryUser {
	if email == h.user.Email {
		return h.user
	}
	return nil
}
func (h *controlInbound) GetUsersCount(context.Context) int64 { return 1 }

type controlInbounds struct {
	inbound.Manager
	handler *controlInbound
}

func (m controlInbounds) ListHandlers(context.Context) []inbound.Handler {
	return []inbound.Handler{m.handler}
}

func (m controlInbounds) GetHandler(context.Context, string) (inbound.Handler, error) {
	return m.handler, nil
}

type controlOutbound struct {
	outbound.Handler
	sender *proxyman.SenderConfig
}

func (h controlOutbound) Tag() string { return "exit" }
func (h controlOutbound) SenderSettings() *serial.TypedMessage {
	return serial.ToTypedMessage(h.sender)
}

func (h controlOutbound) ProxySettings() *serial.TypedMessage {
	return serial.ToTypedMessage(&vless.Account{Id: "00000000-0000-0000-0000-000000000001"})
}

type controlOutbounds struct {
	outbound.Manager
	handler controlOutbound
}

func (m controlOutbounds) ListHandlers(context.Context) []outbound.Handler {
	return []outbound.Handler{m.handler}
}

// These are the field numbers consumed by the fork, independently of the
// candidate's generated client. Keep SenderConfig field 3 reserved.
func controlWireFields(t *testing.T, message proto.Message) map[protowire.Number]protowire.Type {
	t.Helper()
	wire, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[protowire.Number]protowire.Type{}
	for len(wire) > 0 {
		number, kind, n := protowire.ConsumeTag(wire)
		if n < 0 {
			t.Fatal("invalid tag")
		}
		wire = wire[n:]
		n = protowire.ConsumeFieldValue(number, kind, wire)
		if n < 0 {
			t.Fatal("invalid field")
		}
		fields[number] = kind
		wire = wire[n:]
	}
	return fields
}

func TestControlInventoryRealGRPCWire(t *testing.T) {
	user, err := (&protocol.User{Email: "alice@route", Level: 3, Account: serial.ToTypedMessage(&vless.Account{Id: "00000000-0000-0000-0000-000000000001"})}).ToMemoryUser()
	if err != nil {
		t.Fatal(err)
	}
	certificate := &tlsconfig.Certificate{Certificate: []byte("runtime certificate bytes"), Key: []byte("runtime key bytes"), CertificatePath: "/fixture/cert.pem", KeyPath: "/fixture/key.pem", OneTimeLoading: true}
	receiver := &proxyman.ReceiverConfig{StreamSettings: &internet.StreamConfig{SecurityType: "xray.transport.internet.tls.Config", SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(&tlsconfig.Config{Certificate: []*tlsconfig.Certificate{certificate}, UseSystemCa: true})}}}
	sender := &proxyman.SenderConfig{StreamSettings: &internet.StreamConfig{SocketSettings: &internet.SocketConfig{DialerProxy: "trunk"}}}
	s := grpc.NewServer()
	hs := &handlerServer{ihm: controlInbounds{handler: &controlInbound{receiver: receiver, user: user}}, ohm: controlOutbounds{handler: controlOutbound{sender: sender}}}
	RegisterHandlerServiceServer(s, hs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(l) }()
	t.Cleanup(func() { s.Stop(); <-done })
	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewHandlerServiceClient(conn)
	in, err := c.ListInbounds(ctx, &ListInboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Inbounds) != 1 || in.Inbounds[0].Tag != "entry" {
		t.Fatalf("inbounds: %v", in)
	}
	var received proxyman.ReceiverConfig
	if err := proto.Unmarshal(in.Inbounds[0].ReceiverSettings.Value, &received); err != nil {
		t.Fatal(err)
	}
	var security tlsconfig.Config
	if err := proto.Unmarshal(received.StreamSettings.SecuritySettings[0].Value, &security); err != nil {
		t.Fatal(err)
	}
	if len(security.Certificate) != 1 || !proto.Equal(certificate, security.Certificate[0]) || !security.UseSystemCa {
		t.Fatalf("TLS fields lost: %v", &security)
	}
	fields := controlWireFields(t, security.Certificate[0])
	for _, number := range []protowire.Number{1, 2, 5, 6} {
		if fields[number] != protowire.BytesType {
			t.Fatalf("certificate wire field %d changed", number)
		}
	}
	if !bytes.Equal(security.Certificate[0].Certificate, certificate.Certificate) {
		t.Fatal("certificate bytes changed")
	}
	tags, err := c.ListInbounds(ctx, &ListInboundsRequest{IsOnlyTags: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags.Inbounds) != 1 || tags.Inbounds[0].ReceiverSettings != nil || tags.Inbounds[0].ProxySettings != nil {
		t.Fatal("tag-only response leaked settings")
	}
	for _, email := range []string{"", "alice@route"} {
		users, err := c.GetInboundUsers(ctx, &GetInboundUserRequest{Tag: "entry", Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if len(users.Users) != 1 || !proto.Equal(users.Users[0], protocol.ToProtoUser(user)) {
			t.Fatalf("users: %v", users)
		}
	}
	count, err := c.GetInboundUsersCount(ctx, &GetInboundUserRequest{Tag: "entry"})
	if err != nil || count.GetCount() != 1 {
		t.Fatalf("count: %v, %v", count, err)
	}
	out, err := c.ListOutbounds(ctx, &ListOutboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Outbounds) != 1 || out.Outbounds[0].Tag != "exit" || out.Outbounds[0].ProxySettings == nil {
		t.Fatalf("outbounds: %v", out)
	}
	var receivedSender proxyman.SenderConfig
	if err := proto.Unmarshal(out.Outbounds[0].SenderSettings.Value, &receivedSender); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(sender, &receivedSender) {
		t.Fatal("sender settings changed")
	}
	if _, found := controlWireFields(t, &receivedSender)[3]; found {
		t.Fatal("legacy sender field 3 resurrected")
	}
	if !receivedSender.ProtoReflect().Descriptor().ReservedRanges().Has(3) {
		t.Fatal("sender field 3 no longer reserved")
	}
}
