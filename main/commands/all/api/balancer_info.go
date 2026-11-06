package api

import (
	"fmt"
	"io"
	"os"
	"strings"

	routerService "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/main/commands/base"
)

// TODO: support "-json" flag for json output
var cmdBalancerInfo = &base.Command{
	CustomFlags: true,
	UsageLine:   "{{.Exec}} api bi [--server=127.0.0.1:8080] [balancer]...",
	Short:       "Retrieve balancer information",
	Long: `
Retrieve information of specified balancers, including health, strategy and selecting:
each candidate with its status as the balancer reads it, its weight and its share of new
picks (Core spec020). If no balancer tag specified, information for all balancers is returned.

> Ensure that "RoutingService" is enabled under "config.api.services" in the server configuration.

Arguments:

	-s, -server <server:port>
		The API server address. Default 127.0.0.1:8080

	-t, -timeout <seconds>
		Timeout in seconds for calling API. Default 3

Example:

    {{.Exec}} {{.LongName}} --server=127.0.0.1:8080 balancer1 balancer2
`,
	Run: executeBalancerInfo,
}

func executeBalancerInfo(cmd *base.Command, args []string) {
	setSharedFlags(cmd)
	cmd.Flag.Parse(args)
	tags := cmd.Flag.Args()

	conn, ctx, close := dialAPIServer()
	defer close()
	client := routerService.NewRoutingServiceClient(conn)
	// Core spec020 P5: every balancer without a tag, each tag in turn otherwise.
	if len(tags) == 0 {
		resp, err := client.ListBalancers(ctx, &routerService.ListBalancersRequest{})
		if err != nil {
			base.Fatalf("failed to list balancers: %s", err)
		}
		if apiJSON {
			showJSONResponse(resp)
			return
		}
		for _, item := range resp.Balancers {
			writeBalancerInfo(os.Stdout, item.Tag, item.Balancer)
		}
		return
	}
	for _, tag := range tags {
		resp, err := client.GetBalancerInfo(ctx, &routerService.GetBalancerInfoRequest{Tag: tag})
		if err != nil {
			base.Fatalf("failed to get balancer %s: %s", tag, err)
		}
		if apiJSON {
			showJSONResponse(resp)
			continue
		}
		writeBalancerInfo(os.Stdout, tag, resp.Balancer)
	}
}

// writeBalancerInfo prints one balancer: its strategy and status, override, candidates with their
// status, weight and share of new picks (spec020), fallback, and the strategy's selects. A Core before
// spec020 leaves what it does not report out.
func writeBalancerInfo(w io.Writer, tag string, b *routerService.BalancerMsg) {
	const tableIndent = 4
	sb := new(strings.Builder)
	sb.WriteString(tag)
	if b.Strategy != "" {
		sb.WriteString("  " + b.Strategy)
	}
	if b.Status != nil {
		sb.WriteString("  " + statusText(b.Status))
	}
	sb.WriteByte('\n')
	// Override
	if b.Override != nil && b.Override.Target != "" {
		sb.WriteString("  - Selecting Override:\n")
		writeRow(sb, tableIndent, 1, []string{b.Override.Target}, nil)
	}
	// Candidates
	if len(b.Members) > 0 {
		width := len("candidate")
		for _, m := range b.Members {
			width = max(width, len(m.Tag))
		}
		formats := []string{fmt.Sprintf("%%-%ds  ", width), "%-9s ", "%-9s ", "%-7s ", "%s"}
		sb.WriteString("  - Candidates:\n")
		writeRow(sb, tableIndent, 0, []string{"candidate", "kind", "status", "weight", "share"}, formats)
		for i, m := range b.Members {
			kind, status, weight, share := "outbound", "-", "-", "-"
			if m.Balancer {
				kind = "balancer"
			}
			if m.Status != nil {
				status = statusText(m.Status)
				share = fmt.Sprintf("%.1f%%", m.Share*100)
			}
			if m.Weight != 0 {
				weight = fmt.Sprintf("%g", m.Weight)
			}
			writeRow(sb, tableIndent, i+1, []string{m.Tag, kind, status, weight, share}, formats)
		}
	}
	// Fallback
	if f := b.Fallback; f != nil && f.Tag != "" {
		state := ""
		if f.Active {
			state = " (active: new picks use it)"
		}
		sb.WriteString("  - Fallback: " + f.Tag + state + "\n")
	}
	// Selects
	sb.WriteString("  - Selects:\n")
	if b.PrincipleTarget != nil {
		for i, o := range b.PrincipleTarget.Tag {
			writeRow(sb, tableIndent, i+1, []string{o}, nil)
		}
	}
	io.WriteString(w, sb.String())
}

func statusText(s *routerService.BalancerStatus) string {
	switch {
	case !s.Known:
		return "unknown"
	case !s.Alive:
		return "down"
	}
	return fmt.Sprintf("%dms", s.Delay)
}

func getColumnFormats(titles []string) []string {
	w := make([]string, len(titles))
	for i, t := range titles {
		w[i] = fmt.Sprintf("%%-%ds ", len(t))
	}
	return w
}

func writeRow(sb *strings.Builder, indent, index int, values, formats []string) {
	if index == 0 {
		// title line
		sb.WriteString(strings.Repeat(" ", indent+4))
	} else {
		sb.WriteString(fmt.Sprintf("%s%-4d", strings.Repeat(" ", indent), index))
	}
	for i, v := range values {
		format := "%-14s"
		if i < len(formats) {
			format = formats[i]
		}
		sb.WriteString(fmt.Sprintf(format, v))
	}
	sb.WriteByte('\n')
}
