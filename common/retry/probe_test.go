package retry

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestProbePolicyNormalize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy ProbePolicy
		bad    bool
	}{
		{"disabled", ProbePolicy{}, false},
		{"enabled_defaults", ProbePolicy{MaxRetries: 2}, false},
		{"negative", ProbePolicy{MaxRetries: -1}, true},
		{"unbounded", ProbePolicy{MaxRetries: 4}, true},
		{"negative_initial", ProbePolicy{InitialBackoff: -1}, true},
		{"negative_max", ProbePolicy{MaxBackoff: -1}, true},
		{"inverted", ProbePolicy{InitialBackoff: time.Second}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.policy.Normalize()
			if (err != nil) != tc.bad {
				t.Fatalf("Normalize()=%+v, %v", p, err)
			}
			if err == nil && (p.InitialBackoff != 100*time.Millisecond || p.MaxBackoff != 400*time.Millisecond) {
				t.Fatalf("defaults=%+v", p)
			}
		})
	}
}

func TestProbeRetryableErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"eof", io.EOF, true},
		{"unexpected_eof", io.ErrUnexpectedEOF, true},
		{"wrapped_refused", fmt.Errorf("SOCKS5 CONNECT status 5: %w", syscall.ECONNREFUSED), true},
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{"broken_pipe", syscall.EPIPE, true},
		{"dns_timeout", &net.DNSError{IsTimeout: true}, true},
		{"nxdomain", &net.DNSError{IsNotFound: true, IsTemporary: true}, false},
		{"tls_cert", x509.UnknownAuthorityError{}, false},
		{"auth", errors.New("authentication failed"), false},
		{"http", errors.New("HTTP status 503"), false},
		{"asn", errors.New("asn mismatch"), false},
		{"cancel", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"parse_eof", fmt.Errorf("provider: %w", PermanentProbeError{Err: io.EOF}), false},
		{"parse_eof_pointer", &PermanentProbeError{Err: io.EOF}, false},
		{"all_transient", fmt.Errorf("providers: %w", errors.Join(io.EOF, syscall.ECONNREFUSED)), true},
		{"mixed_providers", errors.Join(io.EOF, errors.New("HTTP status 401")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryableProbeError(tc.err); got != tc.want {
				t.Fatalf("retryable=%v, want %v: %v", got, tc.want, tc.err)
			}
		})
	}
}

func TestProbeAttemptsAndBackoff(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		max, failures, wantAttempts int
		failure                     error
		wantError                   bool
	}{
		{"first_success", 2, 0, 1, io.EOF, false},
		{"disabled", 0, 1, 1, io.EOF, true},
		{"recovered", 2, 2, 3, io.EOF, false},
		{"exhausted", 2, 10, 3, io.EOF, true},
		{"terminal", 2, 1, 1, errors.New("HTTP 503"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := (ProbePolicy{MaxRetries: tc.max}).Normalize()
			calls := 0
			var waits []time.Duration
			result, err := p.run(context.Background(), func(context.Context) error {
				calls++
				if calls <= tc.failures {
					return tc.failure
				}
				return nil
			}, func(i int) time.Duration { return 100 * time.Millisecond << i }, func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil })
			if (err != nil) != tc.wantError || calls != tc.wantAttempts || result.Attempts != calls {
				t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
			}
			if len(waits) != calls-1 {
				t.Fatalf("waits=%v calls=%d", waits, calls)
			}
			for i, d := range waits {
				if d != 100*time.Millisecond<<i {
					t.Fatalf("wait[%d]=%v", i, d)
				}
			}
			if tc.failures > 0 && !errors.Is(result.FirstError, tc.failure) {
				t.Fatalf("first error=%v", result.FirstError)
			}
		})
	}
}

func TestProbeBackoffJitterAndCap(t *testing.T) {
	p, _ := (ProbePolicy{MaxRetries: 3}).Normalize()
	for i := 0; i < 20; i++ {
		for sample := 0; sample < 100; sample++ {
			delay := p.backoff(i)
			base := p.InitialBackoff
			if i >= 2 {
				base = p.MaxBackoff
			} else if i == 1 {
				base *= 2
			}
			if delay < base*8/10 || delay > base*12/10 || delay > p.MaxBackoff {
				t.Fatalf("backoff(%d)=%v", i, delay)
			}
		}
	}
	p = ProbePolicy{InitialBackoff: time.Duration(1 << 62), MaxBackoff: time.Duration(1<<63 - 1)}
	for i := 0; i < 10; i++ {
		if d := p.backoff(i); d < 0 || d > p.MaxBackoff {
			t.Fatalf("duration overflow: %v", d)
		}
	}
}

func TestProbeDeadlineAndCancellation(t *testing.T) {
	t.Run("budget_not_reset", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()
		var firstDeadline time.Time
		p, _ := (ProbePolicy{MaxRetries: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}).Normalize()
		calls := 0
		start := time.Now()
		result, err := p.Do(ctx, func(ctx context.Context) error {
			calls++
			deadline, _ := ctx.Deadline()
			if calls == 1 {
				firstDeadline = deadline
				return io.EOF
			}
			if !deadline.Equal(firstDeadline) {
				t.Error("deadline reset")
			}
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || result.Attempts != 2 || time.Since(start) > time.Second {
			t.Fatalf("result=%+v err=%v elapsed=%v", result, err, time.Since(start))
		}
	})
	t.Run("cancel_wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p, _ := (ProbePolicy{MaxRetries: 2}).Normalize()
		waiting := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := p.run(ctx, func(context.Context) error { return io.EOF }, func(int) time.Duration { return time.Hour }, func(ctx context.Context, d time.Duration) error { close(waiting); return waitProbeBackoff(ctx, d) })
			done <- err
		}()
		<-waiting
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("backoff ignored cancellation")
		}
	})
	t.Run("no_budget_for_wait", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p, _ := (ProbePolicy{MaxRetries: 2}).Normalize()
		result, err := p.run(ctx, func(context.Context) error { return io.EOF }, func(int) time.Duration { return time.Hour }, func(context.Context, time.Duration) error { t.Fatal("should not wait"); return nil })
		if result.Attempts != 1 || !errors.Is(err, io.EOF) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("cancel_success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := (ProbePolicy{}).Do(ctx, func(context.Context) error { cancel(); return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatal("published canceled success")
		}
	})
}
