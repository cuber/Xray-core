package api

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/main/commands/base"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// emailList is a repeatable flag.
type emailList []string

func (l *emailList) String() string { return strings.Join(*l, ",") }

func (l *emailList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

var cmdConns = &base.Command{
	CustomFlags: true,
	UsageLine:   "{{.Exec}} api conns [--server=127.0.0.1:8080] [-users] [-user <email>]... [-json] [tag]...",
	Short:       "Retrieve the live connections and sessions of inbounds",
	Long: `
Retrieve each inbound's live objects (Core spec026): stream connections that are not Hy2, their
Mux.Cool and sing-mux carriers, Hy2 QUIC connections, streams and UDP sessions, SS-2022 UDP
sessions, the objects not bound to a user, those being closed, and the streams and protocols of
the inbound's multiplexing servers. Without a tag, every inbound is listed.

> Ensure that "HandlerService" is enabled under "config.api.services" in the server configuration.

Arguments:

	-s, -server <server:port>
		The API server address. Default 127.0.0.1:8080

	-t, -timeout <seconds>
		Timeout in seconds for calling API. Default 3

	-users
		Also list each user with a live object.

	-user <email>
		With -users, only this user; repeatable.

	-json
		Print the response as JSON.

Example:

    {{.Exec}} {{.LongName}} --server=127.0.0.1:8080 -users ss-auto-in
`,
	Run: executeConns,
}

var cmdConnClose = &base.Command{
	CustomFlags: true,
	UsageLine:   "{{.Exec}} api connclose [--server=127.0.0.1:8080] (-user <email>... | -all -tag <tag>...) [-exclude <email>]... [-reason <text>] [-json]",
	Short:       "Close the live connections and sessions of users",
	Long: `
Close the live objects of users on inbounds (Core spec026): their stream connections, carriers
with their streams, Hy2 QUIC connections, the streams and UDP sessions of a relayed Hy2 user, and
SS-2022 UDP sessions. Objects not bound to a user are never closed. Clients may reconnect at once.

> Ensure that "HandlerService" is enabled under "config.api.services" in the server configuration.

Arguments:

	-s, -server <server:port>
		The API server address. Default 127.0.0.1:8080

	-t, -timeout <seconds>
		Timeout in seconds for calling API. Default 3

	-user <email>
		Close this user's objects; repeatable. Without -tag, on every inbound.

	-all
		Close every bound user's objects on the inbounds named by -tag, which it requires.

	-tag <tag>
		Only this inbound; repeatable.

	-exclude <email>
		Leave this user's objects; repeatable.

	-reason <text>
		Logged by the Core with the closure.

	-json
		Print the response as JSON.

Example:

    {{.Exec}} {{.LongName}} --server=127.0.0.1:8080 -all -tag ss-auto-in -exclude admin -reason "move users"
`,
	Run: executeConnClose,
}

func executeConns(cmd *base.Command, args []string) {
	setSharedFlags(cmd)
	userRows := cmd.Flag.Bool("users", false, "")
	var users emailList
	cmd.Flag.Var(&users, "user", "")
	cmd.Flag.Parse(args)

	conn, ctx, close := dialAPIServer()
	defer close()
	request := &handlerService.GetConnectionsRequest{Tags: cmd.Flag.Args(), UserRows: *userRows, Users: users}
	if err := runConns(ctx, os.Stdout, handlerService.NewHandlerServiceClient(conn), request); err != nil {
		base.Fatalf("%s", err)
	}
}

func executeConnClose(cmd *base.Command, args []string) {
	setSharedFlags(cmd)
	all := cmd.Flag.Bool("all", false, "")
	reason := cmd.Flag.String("reason", "", "")
	var users, tags, exclude emailList
	cmd.Flag.Var(&users, "user", "")
	cmd.Flag.Var(&tags, "tag", "")
	cmd.Flag.Var(&exclude, "exclude", "")
	cmd.Flag.Parse(args)
	if cmd.Flag.NArg() > 0 {
		base.Fatalf("unexpected arguments %v: name inbounds with -tag", cmd.Flag.Args())
	}

	conn, ctx, close := dialAPIServer()
	defer close()
	request := &handlerService.CloseConnectionsRequest{Users: users, AllUsers: *all, Tags: tags, ExcludeUsers: exclude, Reason: *reason}
	if err := runConnClose(ctx, os.Stdout, handlerService.NewHandlerServiceClient(conn), request); err != nil {
		base.Fatalf("%s", err)
	}
}

func runConns(ctx context.Context, w io.Writer, client handlerService.HandlerServiceClient, request *handlerService.GetConnectionsRequest) error {
	resp, err := client.GetConnections(ctx, request)
	if status.Code(err) == codes.Unimplemented {
		return fmt.Errorf("connections unavailable: the Core does not implement GetConnections (before spec026)")
	}
	if err != nil {
		return fmt.Errorf("failed to get connections: %w", err)
	}
	if apiJSON {
		showJSONResponse(resp)
		return nil
	}
	for _, in := range resp.Inbounds {
		fmt.Fprintf(w, "%s  %s  unbound %d  closing %d\n", in.Tag, formatConnectionCounts(in.Counts), in.Unbound, in.Closing)
		if in.MuxCoolStreams > 0 || in.SingMuxStreams > 0 || len(in.SingMuxProtocols) > 0 {
			fmt.Fprintf(w, "    streams  mux.cool %d  sing-mux %d%s\n", in.MuxCoolStreams, in.SingMuxStreams, formatProtocols(in.SingMuxProtocols))
		}
		for _, u := range in.Users {
			fmt.Fprintf(w, "    %s  %s\n", u.User, formatConnectionCounts(u.Counts))
		}
		if in.Truncated {
			fmt.Fprintf(w, "    (more users not listed)\n")
		}
	}
	return nil
}

func runConnClose(ctx context.Context, w io.Writer, client handlerService.HandlerServiceClient, request *handlerService.CloseConnectionsRequest) error {
	resp, err := client.CloseConnections(ctx, request)
	if status.Code(err) == codes.Unimplemented {
		return fmt.Errorf("closing connections unavailable: the Core does not implement CloseConnections (before spec026)")
	}
	if err != nil {
		return fmt.Errorf("failed to close connections: %w", err)
	}
	if apiJSON {
		showJSONResponse(resp)
		return nil
	}
	for _, in := range resp.Inbounds {
		fmt.Fprintf(w, "%s  closed %s  pending %d\n", in.Tag, formatConnectionCounts(in.Closed), in.Pending)
	}
	return nil
}

func formatConnectionCounts(c *handlerService.ConnectionCounts) string {
	if c == nil {
		c = &handlerService.ConnectionCounts{}
	}
	return fmt.Sprintf("tcp %d (mux.cool %d, sing-mux %d)  hy2 %d conns %d streams %d udp  udp sessions %d",
		c.Tcp, c.MuxCoolCarriers, c.SingMuxCarriers, c.Hy2Connections, c.Hy2Streams, c.Hy2UdpSessions, c.UdpSessions)
}

func formatProtocols(protocols map[string]uint64) string {
	if len(protocols) == 0 {
		return ""
	}
	names := make([]string, 0, len(protocols))
	for name := range protocols {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %d", name, protocols[name]))
	}
	return "  carriers " + strings.Join(parts, ", ")
}
