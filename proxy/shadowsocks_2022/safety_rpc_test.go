package shadowsocks_2022

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type safetyHandler struct {
	inbound.Handler
	proxy proxy.Inbound
}

func (h safetyHandler) GetInbound() proxy.Inbound              { return h.proxy }
func (h safetyHandler) Tag() string                            { return "ss" }
func (h safetyHandler) ReceiverSettings() *serial.TypedMessage { return nil }
func (h safetyHandler) ProxySettings() *serial.TypedMessage    { return nil }

type safetyInboundManager struct {
	inbound.Manager
	handler inbound.Handler
}

func (m safetyInboundManager) Type() interface{} { return inbound.ManagerType() }
func (m safetyInboundManager) Start() error      { return nil }
func (m safetyInboundManager) Close() error      { return nil }
func (m safetyInboundManager) GetHandler(context.Context, string) (inbound.Handler, error) {
	return m.handler, nil
}

type safetyOutboundManager struct{ outbound.Manager }

func (m safetyOutboundManager) Type() interface{} { return outbound.ManagerType() }
func (m safetyOutboundManager) Start() error      { return nil }
func (m safetyOutboundManager) Close() error      { return nil }

// Use the production HandlerService registration and protobuf operations over
// an actual gRPC transport, while keeping listener/config ownership in this package.
func safetyRPC(t *testing.T, i *MultiUserInbound) command.HandlerServiceClient {
	t.Helper()
	v, err := core.New(&core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = v.AddFeature(safetyInboundManager{handler: safetyHandler{proxy: i}}); err != nil {
		t.Fatal(err)
	}
	if err = v.AddFeature(safetyOutboundManager{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	ctx := context.WithValue(context.Background(), core.XrayKey(1), v)
	obj, err := common.CreateObject(ctx, &command.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	obj.(commander.Service).Register(s)
	ln := bufconn.Listen(1 << 20)
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ln) }()
	t.Cleanup(func() { s.Stop(); _ = ln.Close(); <-done })
	conn, err := grpc.NewClient("passthrough:///ss-safety", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return command.NewHandlerServiceClient(conn)
}

func safetyAlter(t *testing.T, c command.HandlerServiceClient, op *serial.TypedMessage) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.AlterInbound(ctx, &command.AlterInboundRequest{Tag: "ss", Operation: op})
	return err
}
