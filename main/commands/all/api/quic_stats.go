package api

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/main/commands/base"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var cmdQuicStats = &base.Command{
	CustomFlags: true,
	UsageLine:   "{{.Exec}} api quicstats [--server=127.0.0.1:8080] [-connections] [-json] [tag]...",
	Short:       "Retrieve QUIC loss, timers and copies of handlers",
	Long: `
Retrieve the QUIC connections of inbounds and outbounds whose transport builds them (Hy2):
the loss recovery and copy settings in effect, packets and bytes sent, received and lost,
spurious losses, probe timeouts, copies, and duplicates received (Core spec021). Without a
tag, every such handler is listed.

> Ensure that "HandlerService" is enabled under "config.api.services" in the server configuration.

Arguments:

	-s, -server <server:port>
		The API server address. Default 127.0.0.1:8080

	-t, -timeout <seconds>
		Timeout in seconds for calling API. Default 3

	-connections
		Also list each live connection, at most 256 per handler.

	-json
		Print the response as JSON.

Example:

    {{.Exec}} {{.LongName}} --server=127.0.0.1:8080 -connections hy2-out
`,
	Run: executeQuicStats,
}

func executeQuicStats(cmd *base.Command, args []string) {
	setSharedFlags(cmd)
	connections := cmd.Flag.Bool("connections", false, "")
	cmd.Flag.Parse(args)

	conn, ctx, close := dialAPIServer()
	defer close()
	if err := runQuicStats(ctx, os.Stdout, handlerService.NewHandlerServiceClient(conn), *connections, cmd.Flag.Args()); err != nil {
		base.Fatalf("%s", err)
	}
}

// runQuicStats asks for every QUIC handler without tags, for each tag in turn otherwise.
func runQuicStats(ctx context.Context, w io.Writer, client handlerService.HandlerServiceClient, connections bool, tags []string) error {
	requests := []*handlerService.GetQuicStatsRequest{{Connections: connections}}
	if len(tags) > 0 {
		requests = nil
		for _, tag := range tags {
			requests = append(requests, &handlerService.GetQuicStatsRequest{Tag: tag, Connections: connections})
		}
	}
	for _, request := range requests {
		resp, err := client.GetQuicStats(ctx, request)
		if status.Code(err) == codes.Unimplemented {
			return fmt.Errorf("QUIC stats unavailable: the Core does not implement GetQuicStats (before spec021)")
		}
		if err != nil {
			return fmt.Errorf("failed to get QUIC stats %s: %w", request.Tag, err)
		}
		if apiJSON {
			showJSONResponse(resp)
			continue
		}
		for _, h := range resp.Handlers {
			writeQuicHandlerStats(w, h)
		}
	}
	return nil
}

func writeQuicHandlerStats(w io.Writer, h *handlerService.QuicHandlerStats) {
	if r := h.Relay; r != nil {
		// A relay outbound (spec024 R7) has no connections of its own.
		user := r.User
		if user == "" {
			user = "-"
		}
		fmt.Fprintf(w, "%s  %s  %s  relay through %s as %s: %s\n", h.Tag, h.Direction, h.Transport, r.Session, user, r.Status)
		return
	}
	fmt.Fprintf(w, "%s  %s  %s  connections %d active, %d total\n", h.Tag, h.Direction, h.Transport, h.ConnectionsActive, h.ConnectionsTotal)
	if s := h.Session; s != nil {
		fmt.Fprintf(w, "    session  %d–%d connections: %d active, %d draining, %d spare, %d suspect; %d streams open\n",
			s.Min, s.Max, s.Active, s.Draining, s.Spare, s.Suspect, s.StreamsOpen)
	}
	if e := h.Effective; e != nil {
		fmt.Fprintf(w, "    effective  %s\n", formatQuicEffective(e))
	}
	if c := h.Totals; c != nil {
		writeQuicCounters(w, "    ", c)
	}
	for _, c := range h.Connections {
		user := c.User
		if user == "" {
			user = "-"
		}
		pool := ""
		if c.State != "" {
			pool = fmt.Sprintf("  %s, %d streams", c.State, c.StreamsOpen)
		}
		fmt.Fprintf(w, "    %s  %s  age %s  srtt %s  min %s  latest %s  var %s%s\n", c.Remote, user,
			(time.Duration(c.AgeMs) * time.Millisecond).String(), us(c.SmoothedRttUs), us(c.MinRttUs), us(c.LatestRttUs), us(c.RttVarianceUs), pool)
		if c.Counters != nil {
			writeQuicCounters(w, "        ", c.Counters)
		}
		if len(c.Relay) > 0 {
			bindings := make([]string, 0, len(c.Relay))
			for _, b := range c.Relay {
				binding := fmt.Sprintf("%d=%s", b.Index, b.Status)
				if b.User != "" {
					binding += " " + b.User
				}
				bindings = append(bindings, binding)
			}
			fmt.Fprintf(w, "        relay %s\n", strings.Join(bindings, ", "))
		}
	}
	if h.Truncated {
		fmt.Fprintf(w, "    (more connections not listed)\n")
	}
}

func formatQuicEffective(e *handlerService.QuicEffective) string {
	pto := "unlimited"
	if e.PtoBackoffLimit != 0 {
		pto = fmt.Sprint(e.PtoBackoffLimit)
	}
	var copies []string
	if e.DuplicateMaxSize != 0 {
		copies = append(copies, fmt.Sprintf("<=%dB", e.DuplicateMaxSize))
	}
	if e.DuplicateTail {
		copies = append(copies, "tail")
	}
	copyMode := "off"
	if len(copies) > 0 {
		copyMode = strings.Join(copies, "+")
		if e.DuplicateDelayMs != 0 {
			copyMode += fmt.Sprintf(" after %dms", e.DuplicateDelayMs)
		}
	}
	return fmt.Sprintf("maxAckDelay %dms  ackEliciting %d  initialRtt %dms  ptoBackoff %s  lossTime %g  lossPacket %d  copies %s",
		e.MaxAckDelayMs, e.AckElicitingThreshold, e.InitialRttMs, pto, e.LossTimeThreshold, e.LossPacketThreshold, copyMode)
}

func writeQuicCounters(w io.Writer, indent string, c *handlerService.QuicCounters) {
	fmt.Fprintf(w, "%ssent %d pkts %dB  received %d pkts %dB  lost %d pkts %dB  spurious %d  pto %d\n",
		indent, c.PacketsSent, c.BytesSent, c.PacketsReceived, c.BytesReceived, c.PacketsLost, c.BytesLost, c.SpuriousLosses, c.PtoCount)
	fmt.Fprintf(w, "%scopies %d (%dB)  skipped %d  duplicates received %d\n", indent, c.CopiesSent, c.CopyBytesSent, c.CopiesSkipped, c.DuplicatesReceived)
}

func us(v int64) string { return (time.Duration(v) * time.Microsecond).String() }
