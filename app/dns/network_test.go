package dns_test

import (
	"testing"

	"github.com/xtls/xray-core/testing/networktest"
)

func requirePublicDNS(t *testing.T) {
	t.Helper()
	networktest.Enable(t)
}
