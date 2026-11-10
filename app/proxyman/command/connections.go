package command

import (
	"context"
	"sort"
	"strings"
	"time"

	proxyman_inbound "github.com/xtls/xray-core/app/proxyman/inbound"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/features/inbound"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxConnectionUserRows bounds the user rows of one inbound (spec026 R2).
const maxConnectionUserRows = 4096

// closeBound is how long CloseConnections waits for the closes it started (spec026 R3).
const closeBound = 2 * time.Second

// connectionsProvider is an inbound handler with a connection registry (spec026).
type connectionsProvider interface {
	Connections() proxyman_inbound.Connections
	CloseConnections(match func(email string) bool, bound time.Duration) (proxyman_inbound.ConnectionCounts, uint64)
}

// selectInbounds resolves tags to handlers, every handler for none; an unknown tag fails the call.
func (s *handlerServer) selectInbounds(ctx context.Context, tags []string) ([]inbound.Handler, error) {
	if len(tags) == 0 {
		return s.ihm.ListHandlers(ctx), nil
	}
	var handlers []inbound.Handler
	seen := make(map[string]bool)
	for _, tag := range tags {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		h, err := s.ihm.GetHandler(ctx, tag)
		if err != nil {
			return nil, status.Error(codes.NotFound, "no inbound "+tag)
		}
		handlers = append(handlers, h)
	}
	return handlers, nil
}

func emailSet(emails []string) map[string]bool {
	set := make(map[string]bool, len(emails))
	for _, e := range emails {
		if e = strings.TrimSpace(e); e != "" {
			set[strings.ToLower(e)] = true
		}
	}
	return set
}

func connectionCounts(c proxyman_inbound.ConnectionCounts) *ConnectionCounts {
	return &ConnectionCounts{
		Tcp:             c.TCP,
		MuxCoolCarriers: c.MuxCoolCarriers,
		SingMuxCarriers: c.SingMuxCarriers,
		Hy2Connections:  c.Hy2Connections,
		Hy2Streams:      c.Hy2Streams,
		Hy2UdpSessions:  c.Hy2UDPSessions,
		UdpSessions:     c.UDPSessions,
	}
}

// GetConnections reports each selected inbound's live objects, and with user_rows each user's
// (spec026 R2). It is read only.
func (s *handlerServer) GetConnections(ctx context.Context, request *GetConnectionsRequest) (*GetConnectionsResponse, error) {
	handlers, err := s.selectInbounds(ctx, request.Tags)
	if err != nil {
		return nil, err
	}
	only := emailSet(request.Users)
	response := &GetConnectionsResponse{}
	for _, h := range handlers {
		row := &InboundConnections{Tag: h.Tag(), Counts: &ConnectionCounts{}}
		if p, ok := h.(muxStatsProvider); ok {
			stats := p.MuxStats()
			row.MuxCoolStreams = stats.MuxCool.ActiveStreams
			row.SingMuxStreams = stats.SingMux.ActiveTCPStreams + stats.SingMux.ActiveUDPStreams
			row.SingMuxProtocols = stats.SingMux.ActiveCarriers
		}
		if p, ok := h.(connectionsProvider); ok {
			report := p.Connections()
			row.Counts = connectionCounts(report.Counts)
			row.Unbound = report.Unbound
			row.Closing = report.Closing
			if request.UserRows {
				for key, u := range report.Users {
					if len(only) > 0 && !only[key] {
						continue
					}
					row.Users = append(row.Users, &ConnectionUser{User: u.Email, Counts: connectionCounts(u.ConnectionCounts)})
				}
				sort.Slice(row.Users, func(i, j int) bool { return row.Users[i].User < row.Users[j].User })
				if len(row.Users) > maxConnectionUserRows {
					row.Users = row.Users[:maxConnectionUserRows]
					row.Truncated = true
				}
			}
		}
		response.Inbounds = append(response.Inbounds, row)
	}
	sort.Slice(response.Inbounds, func(i, j int) bool { return response.Inbounds[i].Tag < response.Inbounds[j].Tag })
	return response, nil
}

// CloseConnections closes the selected users' live objects on the selected inbounds (spec026 R3).
// Arguments and tags are checked before anything closes.
func (s *handlerServer) CloseConnections(ctx context.Context, request *CloseConnectionsRequest) (*CloseConnectionsResponse, error) {
	users := emailSet(request.Users)
	switch {
	case request.AllUsers && len(users) > 0:
		return nil, status.Error(codes.InvalidArgument, "users and all_users are exclusive")
	case !request.AllUsers && len(users) == 0:
		return nil, status.Error(codes.InvalidArgument, "name users or set all_users")
	case request.AllUsers && len(request.Tags) == 0:
		return nil, status.Error(codes.InvalidArgument, "all_users requires tags")
	}
	handlers, err := s.selectInbounds(ctx, request.Tags)
	if err != nil {
		return nil, err
	}
	exclude := emailSet(request.ExcludeUsers)
	match := func(email string) bool {
		key := strings.ToLower(email)
		if exclude[key] {
			return false
		}
		return request.AllUsers || users[key]
	}

	type result struct {
		tag     string
		closed  proxyman_inbound.ConnectionCounts
		pending uint64
	}
	results := make(chan result, len(handlers))
	var started int
	for _, h := range handlers {
		p, ok := h.(connectionsProvider)
		if !ok {
			continue
		}
		started++
		go func() {
			closed, pending := p.CloseConnections(match, closeBound)
			results <- result{tag: h.Tag(), closed: closed, pending: pending}
		}()
	}
	response := &CloseConnectionsResponse{}
	var total proxyman_inbound.ConnectionCounts
	var pending uint64
	for range started {
		r := <-results
		response.Inbounds = append(response.Inbounds, &InboundClosed{Tag: r.tag, Closed: connectionCounts(r.closed), Pending: r.pending})
		total.TCP += r.closed.TCP
		total.Hy2Connections += r.closed.Hy2Connections
		total.Hy2Streams += r.closed.Hy2Streams
		total.Hy2UDPSessions += r.closed.Hy2UDPSessions
		total.UDPSessions += r.closed.UDPSessions
		pending += r.pending
	}
	sort.Slice(response.Inbounds, func(i, j int) bool { return response.Inbounds[i].Tag < response.Inbounds[j].Tag })

	scope := "users " + strings.Join(request.Users, ",")
	if request.AllUsers {
		scope = "all users"
	}
	if len(request.ExcludeUsers) > 0 {
		scope += " except " + strings.Join(request.ExcludeUsers, ",")
	}
	tags := "every inbound"
	if len(request.Tags) > 0 {
		tags = strings.Join(request.Tags, ",")
	}
	errors.LogWarning(ctx, "CloseConnections (", request.Reason, "): ", scope, " on ", tags, ": closed tcp ", total.TCP,
		", hy2 connections ", total.Hy2Connections, ", hy2 streams ", total.Hy2Streams, ", hy2 udp ", total.Hy2UDPSessions,
		", udp sessions ", total.UDPSessions, ", pending ", pending)
	return response, nil
}
