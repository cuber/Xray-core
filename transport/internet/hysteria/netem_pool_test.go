//go:build linux

package hysteria

import (
	"context"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/session"
)

// Core spec024 FR-010: a session owner's pool on the kernel's netem (testing/netem/run.sh), on the emulated
// 7.5 ms path.

var netemSession = &Session{MinConnections: 1, MaxConnections: 4, StreamsPerConnection: 256, ScaleUpStreams: 128}

func (f *poolFixture) live() int {
	_, _, _, live := f.states()
	return live
}

// 0 -> 600 -> 0 concurrent streams: the pool grows to 4 connections without a stream failing for credit, and drains
// back to one when they close.
func TestNetemPoolScalesWithStreams(t *testing.T) {
	requireNetem(t)
	netem(t)
	f := newPoolFixture(t, netemSession, 1024)
	for i := 0; i < 600; i++ {
		f.hold(t, context.Background()) // fails the test on any open error
	}
	eventually(t, "four connections for 600 streams", func() bool { return f.live() == 4 })
	if report := f.stats.Report(false).Session; report.StreamsOpen != 600 || report.Active != 4 {
		t.Fatalf("at 600 streams: %+v", report)
	}
	f.release()
	f.pool.maintain()
	for round := 0; round < 3; round++ {
		f.advance(61 * time.Second)
		f.pool.maintain() // drains one
		f.pool.maintain() // its last stream closed: a spare
	}
	f.advance(5 * time.Minute)
	f.pool.maintain()
	if active, draining, spare, live := f.states(); active != 1 || draining != 0 || spare != 0 || live != 1 {
		t.Fatalf("after the load: %d active, %d draining, %d spare, %d live", active, draining, spare, live)
	}
}

// probeRoundTrip opens a probe's stream and times one byte through it.
func (f *poolFixture) probeRoundTrip(t *testing.T) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(session.ContextWithProbe(context.Background()), 3*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := f.pool.open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{7}); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(conn, b); err != nil {
		t.Fatalf("probe: %v", err)
	}
	return time.Since(start)
}

// Closing one of two connections keeps the probe alive: probes go to the connection that is left.
func TestNetemPoolProbeSurvivesAClosedConnection(t *testing.T) {
	requireNetem(t)
	netem(t)
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 2, StreamsPerConnection: 16, ScaleUpStreams: 1}, 100)
	f.hold(t, context.Background())
	eventually(t, "a second connection", func() bool { return f.live() == 2 })
	f.pool.snapshot()[0].c.live.Load().CloseWithError(0, "gone")
	for i := 0; i < 10; i++ {
		f.probeRoundTrip(t)
	}
}

// A restarted server: a stream succeeds within one round trip plus 500 ms of the server's return. Streams opened
// before the client hears the old connection's close fail, as on any connection that closes (R5).
func TestNetemPoolReconnectsToARestartedServer(t *testing.T) {
	requireNetem(t)
	netem(t)
	f := newPoolFixture(t, netemSession, 1024)
	f.hold(t, context.Background())
	f.release()
	f.restartServer(t)
	back := time.Now()
	failed := 0
	for {
		if _, err := f.tryHold(context.Background()); err == nil {
			break
		}
		if failed++; time.Since(back) > 2*time.Second {
			t.Fatalf("no stream for 2 s after the server's return (%d attempts)", failed)
		}
	}
	took := time.Since(back)
	t.Logf("a stream %s after the server's return, %d failed on the closing connection", took, failed)
	if took > netemRTT+500*time.Millisecond {
		t.Errorf("reconnected %s after the server's return", took)
	}
	if total := f.stats.Report(false).ConnectionsTotal; total != 2 {
		t.Errorf("%d connections, want the first and its replacement", total)
	}
}

// During a bulk transfer on the session, the probe's delay rises with the queue the transfer builds.
func TestNetemPoolProbeSeesTheSessionsQueue(t *testing.T) {
	requireNetem(t)
	netem(t, "rate", "20mbit", "limit", "2000")
	f := newPoolFixture(t, &Session{MinConnections: 1, MaxConnections: 1, StreamsPerConnection: 256, ScaleUpStreams: 128}, 1024)
	measure := func() time.Duration {
		var samples []time.Duration
		for i := 0; i < 20; i++ {
			samples = append(samples, f.probeRoundTrip(t))
			time.Sleep(20 * time.Millisecond)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2]
	}
	idle := measure()

	bulk := f.hold(t, context.Background())
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the echo comes back on the same stream: read it away
		defer wg.Done()
		io.Copy(io.Discard, bulk)
	}()
	go func() {
		defer wg.Done()
		chunk := make([]byte, 32<<10)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := bulk.Write(chunk); err != nil {
				return
			}
		}
	}()
	time.Sleep(time.Second) // let the transfer fill the path
	loaded := measure()
	close(stop)
	bulk.Close()
	wg.Wait()

	t.Logf("probe median: %s idle, %s during the transfer", idle, loaded)
	if loaded < idle+2*time.Millisecond {
		t.Errorf("the probe did not see the session's queue: %s idle, %s loaded", idle, loaded)
	}
}
