package dns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xtls/xray-core/features/dns"
)

// Core spec007 FR-004: a DNS script answers only once the DNS feature started; a query before fails
// instead of dereferencing the unpublished pool.
func TestDNSScriptBeforeStart(t *testing.T) {
	script := filepath.Join(t.TempDir(), "dns.lua")
	if err := os.WriteFile(script, []byte("function HandleDNSQuery() return {} end"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := newScriptEngine(script)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.query("example.com", dns.IPOption{IPv4Enable: true}); err == nil ||
		!strings.Contains(err.Error(), "not started") {
		t.Fatalf("query before start = %v", err)
	}
	e.close()
}
