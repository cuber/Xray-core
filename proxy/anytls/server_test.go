package anytls

import (
	"context"
	"crypto/sha256"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
)

func testUser(email, password string) *protocol.User {
	return &protocol.User{Email: email, Level: 1, Account: serial.ToTypedMessage(&Account{Password: password})}
}

func TestConfigurationAndAccount(t *testing.T) {
	for name, config := range map[string]*ServerConfig{
		"empty email":          {Users: []*protocol.User{testUser("", "secret")}},
		"empty password":       {Users: []*protocol.User{testUser("alice", "")}},
		"nil user":             {Users: []*protocol.User{nil}},
		"duplicate email":      {Users: []*protocol.User{testUser("alice", "a"), testUser("alice", "b")}},
		"duplicate password":   {Users: []*protocol.User{testUser("alice", "a"), testUser("bob", "a")}},
		"session limit":        {MaxSessions: 4097},
		"user limit":           {MaxSessionsPerUser: 4097},
		"stream limit":         {MaxStreamsPerSession: 1025},
		"padding missing stop": {PaddingScheme: []string{"0=1-20"}},
		"padding duplicate":    {PaddingScheme: []string{"stop=8", "stop=9"}},
		"padding range":        {PaddingScheme: []string{"stop=8", "0=20-1"}},
		"padding overflow":     {PaddingScheme: []string{"stop=8", "0=1-65536"}},
		"padding index alias":  {PaddingScheme: []string{"stop=8", "00=1-20"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	c := &ServerConfig{Users: []*protocol.User{testUser("alice", "secret")}, PaddingScheme: []string{"stop=8", "0=1-20,c,30-40"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	u, err := c.Users[0].ToMemoryUser()
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := protocol.ToProtoUser(u).ToMemoryUser()
	if err != nil || !u.Account.Equals(roundTrip.Account) {
		t.Fatal("account round-trip failed", err)
	}
}

func TestUserRevocationAndBudgets(t *testing.T) {
	s, err := newServer(&ServerConfig{MaxSessionsPerUser: 1, MaxStreamsPerSession: 2}, policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := testUser("alice", "secret").ToMemoryUser()
	ctx := context.Background()
	if err := s.AddUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser(ctx, u); err == nil {
		t.Fatal("duplicate user accepted")
	}
	u.Email = "mutated"
	snapshot := s.GetUser(ctx, "alice")
	snapshot.Email = "also-mutated"
	if s.GetUser(ctx, "alice").Email != "alice" {
		t.Fatal("users are not immutable snapshots")
	}
	makeContext := func() context.Context {
		ctx, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		return context.WithValue(ctx, connectionKey{}, &connection{ctx: ctx, cancel: cancel})
	}
	first, second := makeContext(), makeContext()
	hash := sha256.Sum256([]byte("secret"))
	if _, ok := s.authenticate(first, hash[:]); !ok {
		t.Fatal("authentication failed")
	}
	if _, ok := s.authenticate(second, hash[:]); ok {
		t.Fatal("per-user session limit bypassed")
	}
	reason := second.Value(connectionKey{}).(*connection).admissionFailure
	if !strings.Contains(reason, "sessions=1 limit=1") || strings.Contains(reason, "secret") {
		t.Fatalf("missing or unsafe admission diagnostic: %s", reason)
	}
	if !s.openStream(first) || !s.openStream(first) || s.openStream(first) {
		t.Fatal("per-session stream limit failed")
	}
	s.closeStream(first)
	s.closeStream(first)
	s.activeStreams = maxActiveStreams
	if s.openStream(first) {
		t.Fatal("shared protocol buffer budget bypassed")
	}
	s.activeStreams = 0
	old := first.Value(connectionKey{}).(*connection).user
	if err := s.RemoveUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	u, _ = testUser("alice", "secret").ToMemoryUser()
	if err := s.AddUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if s.openStream(first) || s.users["alice"] == old {
		t.Fatal("old generation regained access")
	}
	if _, ok := s.authenticate(second, hash[:]); !ok {
		t.Fatal("new generation rejected")
	}
}

func TestUserConcurrentMutations(t *testing.T) {
	s, _ := newServer(&ServerConfig{}, policy.DefaultManager{})
	defer s.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				u, _ := testUser("alice", "secret").ToMemoryUser()
				s.AddUser(context.Background(), u)
				s.GetUsers(context.Background())
				s.GetUsersCount(context.Background())
				s.RemoveUser(context.Background(), "alice")
			}
		}()
	}
	wg.Wait()
}

func TestRevocationAdmissionBarrier(t *testing.T) {
	s, err := newServer(&ServerConfig{}, policy.DefaultManager{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := testUser("barrier", "old-secret").ToMemoryUser()
	if err := s.AddUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := net.Pipe()
	defer b.Close()
	c := &connection{Conn: a, ctx: ctx, cancel: cancel}
	ctx = context.WithValue(ctx, connectionKey{}, c)
	s.connections[c] = struct{}{}
	hash := sha256.Sum256([]byte("old-secret"))
	if _, ok := s.authenticate(ctx, hash[:]); !ok {
		t.Fatal("initial authentication rejected")
	}
	start, removed := make(chan struct{}), make(chan struct{})
	var ready, workers sync.WaitGroup
	var late atomic.Int32
	for range 100 {
		ready.Add(1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready.Done()
			<-start
			if s.openStream(ctx) {
				s.closeStream(ctx)
			}
			<-removed
			if s.openStream(ctx) {
				late.Add(1)
				s.closeStream(ctx)
			}
		}()
	}
	ready.Wait()
	close(start)
	if err := s.RemoveUser(context.Background(), "barrier"); err != nil {
		t.Fatal(err)
	}
	replacement, _ := testUser("barrier", "new-secret").ToMemoryUser()
	if err := s.AddUser(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	close(removed)
	workers.Wait()
	if late.Load() != 0 || s.activeStreams != 0 || ctx.Err() == nil {
		t.Fatalf("revocation leaked admission: late=%d active=%d canceled=%v", late.Load(), s.activeStreams, ctx.Err())
	}
	// A credential read before removal must be looked up again at admission.
	// Lookup and registration share s.mu; there is no unlocked intermediate state.
	fresh, stop := context.WithCancel(context.Background())
	defer stop()
	fresh = context.WithValue(fresh, connectionKey{}, &connection{ctx: fresh, cancel: stop})
	if _, ok := s.authenticate(fresh, hash[:]); ok {
		t.Fatal("stale credentials authenticated after replacement")
	}
	newHash := sha256.Sum256([]byte("new-secret"))
	if _, ok := s.authenticate(fresh, newHash[:]); !ok {
		t.Fatal("replacement credentials rejected")
	}
}

func TestStreamContextIsolation(t *testing.T) {
	parent := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "test"})
	parent = session.ContextWithOutbounds(parent, []*session.Outbound{{Name: "parent"}})
	u, _ := testUser("alice", "secret").ToMemoryUser()
	a, b := streamContext(parent, *u, nil), streamContext(parent, *u, nil)
	session.InboundFromContext(a).User.Email = "changed"
	session.OutboundsFromContext(a)[0].Name = "changed"
	if session.InboundFromContext(b).User.Email != "alice" || session.OutboundsFromContext(b)[0].Name != "" || session.InboundFromContext(parent).User != nil {
		t.Fatal("multiplexed stream contexts share mutable state")
	}
}

func FuzzPaddingValidation(f *testing.F) {
	f.Add("stop=8", "0=10-20")
	f.Add("stop=0", "0=999999999999-1")
	f.Fuzz(func(t *testing.T, a, b string) {
		_ = (&ServerConfig{PaddingScheme: []string{a, b}}).Validate()
	})
}
