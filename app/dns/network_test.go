package dns_test

import (
	"github.com/xtls/xray-core/testing/networktest"
	"testing"
)

func requirePublicDNS(t *testing.T) {
	t.Helper()
	networktest.Enable(t)
}
