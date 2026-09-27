package api

import (
	"testing"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/anytls"
)

func TestExtractAnyTLSInboundUsers(t *testing.T) {
	want := &protocol.User{Email: "cli-test", Level: 7, Account: serial.ToTypedMessage(&anytls.Account{Password: "test-only"})}
	config := &core.InboundHandlerConfig{ProxySettings: serial.ToTypedMessage(&anytls.ServerConfig{Users: []*protocol.User{want}})}
	users := extractInboundUsers(config)
	if len(users) != 1 || users[0].Email != want.Email || users[0].Level != want.Level {
		t.Fatal("AnyTLS users were not preserved by the CLI decoder")
	}
	account, err := users[0].Account.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := account.(*anytls.Account); !ok || value.Password != "test-only" {
		t.Fatal("AnyTLS account was lost by the CLI decoder")
	}
}
