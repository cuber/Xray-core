package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"syscall"
	"time"
)

// ProbePolicy applies only to idempotent health probes, not protocol dialers.
// Callers supply one deadline spanning all attempts and backoff waits.
type ProbePolicy struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type ProbeResult struct {
	Attempts   int
	FirstError error
}

// PermanentProbeError preserves a response/parser error for diagnostics while
// explicitly excluding it from transport retries (including a parser's EOF).
type PermanentProbeError struct{ Err error }

func (e PermanentProbeError) Error() string { return e.Err.Error() }
func (e PermanentProbeError) Unwrap() error { return e.Err }

func (p ProbePolicy) Normalize() (ProbePolicy, error) {
	if p.MaxRetries < 0 || p.MaxRetries > 3 {
		return p, fmt.Errorf("probe retry maxRetries must be between 0 and 3")
	}
	if p.InitialBackoff < 0 || p.MaxBackoff < 0 {
		return p, fmt.Errorf("probe retry backoff must be positive")
	}
	if p.InitialBackoff == 0 {
		p.InitialBackoff = 100 * time.Millisecond
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = 400 * time.Millisecond
	}
	if p.MaxBackoff < p.InitialBackoff {
		return p, fmt.Errorf("probe retry maxBackoff must be >= initialBackoff")
	}
	return p, nil
}

// Do counts application-level attempts. HTTP transports may also retry a stale
// connection internally. Operations must observe ctx; Do never spawns workers.
func (p ProbePolicy) Do(ctx context.Context, operation func(context.Context) error) (ProbeResult, error) {
	policy, err := p.Normalize()
	if err != nil {
		return ProbeResult{}, err
	}
	return policy.run(ctx, operation, policy.backoff, waitProbeBackoff)
}

func (p ProbePolicy) run(ctx context.Context, operation func(context.Context) error, backoff func(int) time.Duration, wait func(context.Context, time.Duration) error) (ProbeResult, error) {
	var result ProbeResult
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Attempts++
		err := operation(ctx)
		if err == nil {
			// Do not publish a success completed after cancellation/deadline.
			return result, ctx.Err()
		}
		if result.FirstError == nil {
			result.FirstError = err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if result.Attempts > p.MaxRetries || !IsRetryableProbeError(err) {
			return result, err
		}
		delay := backoff(result.Attempts - 1)
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
			// There is no budget left for another attempt after this wait.
			return result, err
		}
		if err := wait(ctx, delay); err != nil {
			return result, err
		}
	}
}

func (p ProbePolicy) backoff(retryIndex int) time.Duration {
	delay := p.InitialBackoff
	for i := 0; i < retryIndex; i++ {
		if delay >= p.MaxBackoff || delay > p.MaxBackoff/2 {
			delay = p.MaxBackoff
			break
		}
		delay *= 2
	}
	// Integer arithmetic keeps +/-20% jitter safe even for very large durations.
	delta := delay / 5
	low := delay - delta
	span := delta * 2
	if headroom := p.MaxBackoff - low; span > headroom {
		span = headroom
	}
	return low + time.Duration(rand.Int64N(int64(span)+1))
}

func waitProbeBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// IsRetryableProbeError deliberately does not use text matching or a broad
// "network" category: TLS/auth/config/HTTP/ASN errors must remain terminal.
func IsRetryableProbeError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	switch err.(type) {
	case PermanentProbeError, *PermanentProbeError:
		return false
	}
	if group, ok := err.(interface{ Unwrap() []error }); ok {
		children := group.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !IsRetryableProbeError(child) {
				return false
			}
		}
		return true
	}
	if cause := errors.Unwrap(err); cause != nil {
		return IsRetryableProbeError(cause)
	}
	if dns, ok := err.(*net.DNSError); ok {
		return !dns.IsNotFound && (dns.IsTimeout || dns.IsTemporary)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
