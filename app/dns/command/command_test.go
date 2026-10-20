package command_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dns/command"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	_ "github.com/xtls/xray-core/main/distro/all"
	"golang.org/x/net/dns/dnsmessage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// startH2CDoH answers every A query with 192.0.2.1 over HTTP/2 prior knowledge.
func startH2CDoH(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parser dnsmessage.Parser
		header, err := parser.Start(raw)
		if err != nil {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		question, _ := parser.Question()
		builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, RecursionAvailable: true})
		_ = builder.StartQuestions()
		_ = builder.Question(question)
		_ = builder.StartAnswers()
		if question.Type == dnsmessage.TypeA {
			_ = builder.AResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 300},
				dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}})
		}
		answer, _ := builder.Finish()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(answer)
	})}
	server.Protocols = new(http.Protocols)
	server.Protocols.SetUnencryptedHTTP2(true)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

func startInstance(t *testing.T, config string) *core.Instance {
	t.Helper()
	instance, err := core.StartInstance("json", []byte(config))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func dnsClient(t *testing.T, apiPort int) command.DNSServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", apiPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return command.NewDNSServiceClient(conn)
}

func listServers(t *testing.T, client command.DNSServiceClient) (*command.ListServersResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var response *command.ListServersResponse
	var err error
	// The API listener starts with the instance; allow it a moment to accept.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if response, err = client.ListServers(ctx, &command.ListServersRequest{}); status.Code(err) != codes.Unavailable {
			break
		}
	}
	return response, err
}

func TestListServersReportsLoadedServersAndCaches(t *testing.T) {
	doh, api := startH2CDoH(t), freePort(t)
	instance := startInstance(t, fmt.Sprintf(`{
		"log": {"loglevel": "none"},
		"api": {"tag": "api", "listen": "127.0.0.1:%[2]d", "services": ["DNSService"]},
		"dns": {
			"queryStrategy": "UseIPv4",
			"hosts": {"static.example": "198.51.100.1"},
			"servers": [
				{"address": "h2c+local://127.0.0.1:%[1]d/dns-query?ob=exit", "outboundTags": ["exit-b", "exit-a"], "tag": "dns-egress"},
				"h2c+local://user:secret@127.0.0.1:%[1]d/dns-query",
				{"address": "8.8.8.8", "disableCache": true, "domains": ["domain:example.org"], "skipFallback": true}
			]
		},
		"outbounds": [{"tag": "direct", "protocol": "freedom"}]
	}`, doh, api))
	resolver := instance.GetFeature(dns.ClientType()).(dns.Client)
	if _, _, err := resolver.LookupIP("bound.example", dns.IPOption{IPv4Enable: true, OutboundTag: "exit-a"}); err != nil {
		t.Fatalf("bound lookup: %v", err)
	}
	for _, name := range []string{"one.example", "two.example"} {
		if _, _, err := resolver.LookupIP(name, dns.IPOption{IPv4Enable: true}); err != nil {
			t.Fatalf("lookup %s: %v", name, err)
		}
	}

	response, err := listServers(t, dnsClient(t, api))
	if err != nil {
		t.Fatal(err)
	}
	if response.SystemResolver || response.QueryStrategy != "UseIPv4" || response.StaticHosts != 1 || response.Script {
		t.Fatalf("global options = %+v", response)
	}
	if len(response.Servers) != 3 {
		t.Fatalf("servers = %v", response.Servers)
	}
	bound, plain, udp := response.Servers[0], response.Servers[1], response.Servers[2]
	if bound.Index != 0 || bound.Address != fmt.Sprintf("h2c+local://127.0.0.1:%d/dns-query?ob=exit", doh) ||
		bound.Tag != "dns-egress" || fmt.Sprint(bound.OutboundTags) != "[exit-a exit-b]" ||
		bound.QueryStrategy != "UseIPv4" || bound.DisableCache || bound.CacheEntries != 1 {
		t.Fatalf("bound server = %+v", bound)
	}
	if plain.Address != fmt.Sprintf("h2c+local://127.0.0.1:%d/dns-query", doh) || len(plain.OutboundTags) != 0 || plain.CacheEntries != 2 {
		t.Fatalf("unrestricted server = %+v; userinfo must be stripped and two names cached", plain)
	}
	if udp.Address != "udp://8.8.8.8:53" || !udp.DisableCache || udp.CacheEntries != 0 || !udp.SkipFallback || udp.DomainRules != 1 ||
		udp.TimeoutMs != 4000 || plain.Name == "" {
		t.Fatalf("udp server = %+v", udp)
	}
}

func TestListServersDefaultConfigUsesSystemResolver(t *testing.T) {
	api := freePort(t)
	startInstance(t, fmt.Sprintf(`{"log": {"loglevel": "none"},
		"api": {"tag": "api", "listen": "127.0.0.1:%d", "services": ["DNSService"]},
		"outbounds": [{"tag": "direct", "protocol": "freedom"}]}`, api))
	response, err := listServers(t, dnsClient(t, api))
	if err != nil {
		t.Fatal(err)
	}
	if !response.SystemResolver || len(response.Servers) != 0 {
		t.Fatalf("default config = %+v", response)
	}
}

func TestListServersIsUnimplementedWhenNotEnabled(t *testing.T) {
	api := freePort(t)
	startInstance(t, fmt.Sprintf(`{"log": {"loglevel": "none"},
		"api": {"tag": "api", "listen": "127.0.0.1:%d", "services": ["HandlerService"]},
		"dns": {"servers": ["8.8.8.8"]},
		"outbounds": [{"tag": "direct", "protocol": "freedom"}]}`, api))
	if _, err := listServers(t, dnsClient(t, api)); status.Code(err) != codes.Unimplemented {
		t.Fatalf("ListServers without DNSService = %v, want Unimplemented", err)
	}
}
