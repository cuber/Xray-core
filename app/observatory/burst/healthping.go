package burst

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/dice"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/retry"
	"github.com/xtls/xray-core/features/routing"
)

// HealthPingSettings holds settings for health Checker
type HealthPingSettings struct {
	Destination      string            `json:"destination"`
	Connectivity     string            `json:"connectivity"`
	Interval         time.Duration     `json:"interval"`
	SamplingCount    int               `json:"sampling"`
	Timeout          time.Duration     `json:"timeout"`
	HttpMethod       string            `json:"httpMethod"`
	KeepAlive        bool              `json:"keepAlive"`
	HTTP2            bool              `json:"http2"`
	Retry            retry.ProbePolicy `json:"retry"`
	ProbeOutboundTag string            `json:"probeOutboundTag,omitempty"`
	// DestinationsByPrefix overrides the probe URL for outbound tags matching
	// the given prefix. Longest matching prefix wins; empty map means all tags
	// use Destination.
	DestinationsByPrefix map[string]string `json:"destinationsByPrefix,omitempty"`

	// The scheduler's cold start (Core spec027 TL-10): samples recorded after the unrecorded first probe,
	// and the wait between them. Not configurable; NewHealthPing sets them.
	coldSamples int
	coldSpacing time.Duration
}

const (
	coldStartSamples = 4
	coldStartSpacing = time.Second
)

// HealthPing is the health checker for balancers
type HealthPing struct {
	ctx         context.Context
	dispatcher  routing.Dispatcher
	access      sync.Mutex
	ticker      *time.Ticker
	tickerClose chan struct{}
	schedulerMu sync.Mutex
	cancel      context.CancelFunc
	workers     sync.WaitGroup

	// onChange runs after a result is recorded or removed, outside access (Core spec027 TL-5).
	onChange func()

	// Selector is the tag-prefix selector this HealthPing owns. Set by
	// Manager when building multi-group observatories; unused by legacy
	// single-group callers (who pass the selector via StartScheduler's
	// closure instead).
	Selector []string

	Settings *HealthPingSettings
	Results  map[string]*HealthPingRTTS
}

// NewHealthPing creates a new HealthPing with settings
func NewHealthPing(ctx context.Context, dispatcher routing.Dispatcher, config *HealthPingConfig) *HealthPing {
	settings := &HealthPingSettings{}
	if config != nil {
		var retryPolicy retry.ProbePolicy
		if config.Retry != nil {
			retryPolicy = retry.ProbePolicy{MaxRetries: int(config.Retry.MaxRetries), InitialBackoff: time.Duration(config.Retry.InitialBackoff), MaxBackoff: time.Duration(config.Retry.MaxBackoff)}
		}

		var httpMethod string
		if config.HttpMethod == "" {
			httpMethod = "HEAD"
		} else {
			httpMethod = strings.TrimSpace(config.HttpMethod)
		}

		var destByPrefix map[string]string
		if len(config.DestinationsByPrefix) > 0 {
			destByPrefix = make(map[string]string, len(config.DestinationsByPrefix))
			for k, v := range config.DestinationsByPrefix {
				destByPrefix[k] = strings.TrimSpace(v)
			}
		}
		settings = &HealthPingSettings{
			Connectivity:         strings.TrimSpace(config.Connectivity),
			Destination:          strings.TrimSpace(config.Destination),
			Interval:             time.Duration(config.Interval),
			SamplingCount:        int(config.SamplingCount),
			Timeout:              time.Duration(config.Timeout),
			HttpMethod:           httpMethod,
			KeepAlive:            config.KeepAlive,
			HTTP2:                config.Http2,
			Retry:                retryPolicy,
			ProbeOutboundTag:     strings.TrimSpace(config.ProbeOutboundTag),
			DestinationsByPrefix: destByPrefix,
		}
	}
	if settings.Destination == "" {
		// Destination URL, need 204 for success return default to chromium
		// https://github.com/chromium/chromium/blob/main/components/safety_check/url_constants.cc#L10
		// https://chromium.googlesource.com/chromium/src/+/refs/heads/main/components/safety_check/url_constants.cc#10
		settings.Destination = "https://connectivitycheck.gstatic.com/generate_204"
	}
	if settings.Interval == 0 {
		settings.Interval = time.Duration(1) * time.Minute
	} else if settings.Interval < 10 {
		errors.LogWarning(ctx, "health check interval is too small, 10s is applied")
		settings.Interval = time.Duration(10) * time.Second
	}
	if settings.SamplingCount <= 0 {
		settings.SamplingCount = 10
	}
	if settings.Timeout <= 0 {
		// results are saved after all health pings finish,
		// a larger timeout could possibly makes checks run longer
		settings.Timeout = time.Duration(5) * time.Second
	}
	settings.coldSamples, settings.coldSpacing = coldStartSamples, coldStartSpacing
	return &HealthPing{
		ctx:        ctx,
		dispatcher: dispatcher,
		Settings:   settings,
		Results:    nil,
	}
}

// StartScheduler implements the HealthChecker. selector must be concurrency-safe,
// return promptly, and release its own resources: its legacy signature cannot
// accept cancellation.
// Stop cancels waiting for a selector, but cannot terminate the callback itself.
func (h *HealthPing) StartScheduler(selector func() ([]string, error)) {
	h.schedulerMu.Lock()
	defer h.schedulerMu.Unlock()
	if h.ticker != nil {
		return
	}
	interval := h.Settings.Interval * time.Duration(h.Settings.SamplingCount)
	ticker := time.NewTicker(interval)
	tickerClose := make(chan struct{})
	h.ticker = ticker
	h.tickerClose = tickerClose
	ctx, cancel := context.WithCancel(h.ctx)
	h.cancel = cancel
	// Scheduled probes that fall due during the cold start wait for its end (TL-10).
	cold := make(chan struct{})
	h.workers.Add(2)
	go func() {
		defer h.workers.Done()
		defer close(cold)
		tags, err := resolvePingTags(ctx, selector)
		if err != nil {
			errors.LogWarning(h.ctx, "error select outbounds for initial health check: ", err)
			return
		}
		h.coldStart(ctx, tags)
	}()

	go func() {
		defer h.workers.Done()
		var cancelPrevious context.CancelFunc
		for {
			// A new round supersedes delayed and in-flight work from the old one.
			// Use the publication lock so canceled rounds cannot publish afterward.
			h.access.Lock()
			if cancelPrevious != nil {
				cancelPrevious()
			}
			roundCtx, cancelRound := context.WithCancel(ctx)
			cancelPrevious = cancelRound
			h.access.Unlock()
			h.workers.Add(1)
			go func() {
				defer h.workers.Done()
				defer cancelRound()
				tags, err := resolvePingTags(roundCtx, selector)
				if err != nil {
					errors.LogWarning(h.ctx, "error select outbounds for scheduled health check: ", err)
					return
				}
				h.doCheckContext(roundCtx, tags, interval, h.Settings.SamplingCount, cold)
				h.access.Lock()
				cleaned := roundCtx.Err() == nil
				if cleaned {
					h.cleanupLocked(tags)
				}
				h.access.Unlock()
				if cleaned {
					h.changed() // Core spec027 TL-5: removed results change the observation
				}
			}()
			select {
			case <-ticker.C:
				continue
			case <-tickerClose:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopScheduler implements the HealthChecker
func (h *HealthPing) StopScheduler() {
	h.schedulerMu.Lock()
	defer h.schedulerMu.Unlock()
	if h.ticker == nil {
		return
	}
	h.ticker.Stop()
	// Serialize cancellation with publishing scheduled results.
	h.access.Lock()
	h.cancel()
	h.access.Unlock()
	h.ticker = nil
	close(h.tickerClose)
	h.workers.Wait()
	h.tickerClose = nil
	h.cancel = nil
}

func resolvePingTags(ctx context.Context, selector func() ([]string, error)) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		tags []string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		if ctx.Err() != nil {
			return
		}
		tags, err := selector()
		done <- result{tags: tags, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.tags, result.err
	}
}

// Check implements the HealthChecker
func (h *HealthPing) Check(tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	errors.LogInfo(h.ctx, "perform one-time health check for tags ", tags)
	h.doCheck(tags, 0, 1)
	return nil
}

type rtt struct {
	handler string
	value   time.Duration
}

// doCheck performs the 'rounds' amount checks in given 'duration'. You should make
// sure all tags are valid for current balancer
func (h *HealthPing) doCheck(tags []string, duration time.Duration, rounds int) {
	h.doCheckContext(h.ctx, tags, duration, rounds, nil)
}

// doCheckContext performs the checks of doCheck under ctx. A probe that falls due before after is closed
// waits for it; nil waits for nothing.
func (h *HealthPing) doCheckContext(ctx context.Context, tags []string, duration time.Duration, rounds int, after <-chan struct{}) {
	count := len(tags) * rounds
	if count == 0 {
		return
	}
	ch := make(chan *rtt, count)
	var probes sync.WaitGroup

	for _, tag := range tags {
		client, handler, destination := h.pingClientFor(ctx, tag)
		defer client.CloseIdleConnections()
		for i := 0; i < rounds; i++ {
			delay := time.Duration(0)
			if duration > 0 {
				delay = time.Duration(dice.RollInt63n(int64(duration)))
			}
			probes.Add(1)
			go func() {
				defer probes.Done()
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if after != nil {
					select {
					case <-ctx.Done():
						return
					case <-after:
					}
				}
				if r := h.probe(ctx, client, tag, handler, destination); r != nil {
					ch <- r
				}
			}()
		}
	}
	// Join canceled delayed samples and HTTP requests before closing their clients.
	defer probes.Wait()
	for i := 0; i < count; i++ {
		var rtt *rtt
		select {
		case <-ctx.Done():
			return
		case rtt = <-ch:
		}
		if ctx.Err() != nil {
			return
		}
		h.record(ctx, rtt)
	}
}

// coldStart is the scheduler's first check (Core spec027 TL-10). An outbound's first probe may set up its
// connection (a QUIC handshake and authentication, a Mux connection), which later probes reuse, so a successful
// first probe is not recorded. coldSamples probes follow, the first at once and the others coldSpacing apart,
// so a window starts from several samples of the reused connection. A failure is recorded and ends that
// outbound's cold start, as one initial check would have; the scheduled rounds continue from there.
func (h *HealthPing) coldStart(ctx context.Context, tags []string) {
	var outbounds sync.WaitGroup
	for _, tag := range tags {
		outbounds.Add(1)
		go func() {
			defer outbounds.Done()
			client, handler, destination := h.pingClientFor(ctx, tag)
			defer client.CloseIdleConnections()
			first := h.probe(ctx, client, tag, handler, destination)
			if first == nil {
				return
			}
			if !first.succeeded() {
				h.record(ctx, first)
				return
			}
			errors.LogDebug(h.ctx, "cold start: first probe of ", tag, " took ", first.value, ", not recorded")
			for i := 0; i < h.Settings.coldSamples; i++ {
				if i > 0 {
					timer := time.NewTimer(h.Settings.coldSpacing)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
				r := h.probe(ctx, client, tag, handler, destination)
				if r == nil {
					return
				}
				h.record(ctx, r)
				if !r.succeeded() {
					return
				}
			}
		}()
	}
	outbounds.Wait()
}

// pingClientFor is the client that probes tag, with the handler it dials and the URL it requests.
func (h *HealthPing) pingClientFor(ctx context.Context, tag string) (*pingClient, string, string) {
	handler := tag
	if h.Settings.ProbeOutboundTag != "" {
		handler = h.Settings.ProbeOutboundTag
	}
	destination := h.Settings.destinationFor(tag)
	client := newPingClient(
		ctx,
		h.dispatcher,
		destination,
		h.Settings.Timeout,
		handler,
		h.Settings.KeepAlive,
		h.Settings.HTTP2,
	)
	client.retryPolicy = h.Settings.Retry
	return client, handler, destination
}

// probe measures one sample of tag: its delay, rttFailed, or 0 when the network is down. It returns nil when
// ctx ended first.
func (h *HealthPing) probe(ctx context.Context, client *pingClient, tag, handler, destination string) *rtt {
	errors.LogDebug(h.ctx, "checking ", handler)
	delay, err := client.MeasureDelay(h.Settings.HttpMethod)
	if err == nil {
		return &rtt{handler: tag, value: delay}
	}
	if ctx.Err() != nil {
		return nil
	}
	if !h.checkConnectivityContext(ctx) {
		errors.LogWarning(h.ctx, "network is down")
		return &rtt{handler: tag, value: 0}
	}
	errors.LogWarning(h.ctx, fmt.Sprintf(
		"error ping %s with %s: %s",
		destination,
		handler,
		err,
	))
	return &rtt{handler: tag, value: rttFailed}
}

func (r *rtt) succeeded() bool {
	return r.value > 0 && r.value != rttFailed
}

// record publishes a sample unless ctx has ended. Nothing is put while the network is down.
func (h *HealthPing) record(ctx context.Context, r *rtt) {
	if r.value <= 0 {
		return
	}
	h.access.Lock()
	put := ctx.Err() == nil
	if put {
		h.putResultLocked(r.handler, r.value)
	}
	h.access.Unlock()
	if put {
		h.changed()
	}
}

// PutResult put a ping rtt to results
func (h *HealthPing) PutResult(tag string, rtt time.Duration) {
	h.access.Lock()
	h.putResultLocked(tag, rtt)
	h.access.Unlock()
	h.changed()
}

// changed runs onChange, if any (Core spec027 TL-5).
func (h *HealthPing) changed() {
	if h.onChange != nil {
		h.onChange()
	}
}

func (h *HealthPing) putResultLocked(tag string, rtt time.Duration) {
	if h.Results == nil {
		h.Results = make(map[string]*HealthPingRTTS)
	}
	r, ok := h.Results[tag]
	if !ok {
		// validity is 2 times to sampling period, since the check are
		// distributed in the time line randomly, in extreme cases,
		// Previous checks are distributed on the left, and later ones
		// on the right
		validity := h.Settings.Interval * time.Duration(h.Settings.SamplingCount) * 2
		r = NewHealthPingResult(h.Settings.SamplingCount, validity)
		h.Results[tag] = r
	}
	r.Put(rtt)
}

// Cleanup removes results of removed handlers,
// tags should be all valid tags of the Balancer now
func (h *HealthPing) Cleanup(tags []string) {
	h.access.Lock()
	h.cleanupLocked(tags)
	h.access.Unlock()
	h.changed()
}

func (h *HealthPing) cleanupLocked(tags []string) {
	for tag := range h.Results {
		found := false
		for _, v := range tags {
			if tag == v {
				found = true
				break
			}
		}
		if !found {
			delete(h.Results, tag)
		}
	}
}

// destinationFor returns the probe URL for the given outbound tag. It picks
// the longest DestinationsByPrefix entry whose key is a prefix of tag; if no
// prefix matches it returns the global Destination.
func (s *HealthPingSettings) destinationFor(tag string) string {
	if len(s.DestinationsByPrefix) == 0 {
		return s.Destination
	}
	bestKey := ""
	bestVal := ""
	for prefix, url := range s.DestinationsByPrefix {
		if url == "" {
			continue
		}
		if strings.HasPrefix(tag, prefix) && len(prefix) >= len(bestKey) {
			bestKey = prefix
			bestVal = url
		}
	}
	if bestVal == "" {
		return s.Destination
	}
	return bestVal
}

// checkConnectivity checks the network connectivity, it returns
// true if network is good or "connectivity check url" not set
func (h *HealthPing) checkConnectivity() bool {
	return h.checkConnectivityContext(h.ctx)
}

func (h *HealthPing) checkConnectivityContext(ctx context.Context) bool {
	if h.Settings.Connectivity == "" {
		return true
	}
	tester := newDirectPingClient(
		h.Settings.Connectivity,
		h.Settings.Timeout,
	)
	tester.ctx = ctx
	defer tester.CloseIdleConnections()
	if _, err := tester.MeasureDelay(h.Settings.HttpMethod); err != nil {
		return false
	}
	return true
}
