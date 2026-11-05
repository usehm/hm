package provider

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

// Backoff between retries: 1s, 2s, 4s ... up to maxDelay, give or take a
// fifth so many clients do not retry in step.
const (
	baseDelay = time.Second
	maxDelay  = 60 * time.Second
)

// Retry retries another provider's calls that failed for a passing
// reason: rate limits, overload, server errors and dropped connections.
// A call is retried only while nothing of it has been streamed, so the
// user never sees an answer twice.
type Retry struct {
	Provider
	// Max is how many retries follow the first failure; 0 retries nothing.
	Max int
	// sleep waits d or until ctx ends; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// WithRetry wraps p; max <= 0 returns p as is.
func WithRetry(p Provider, max int) Provider {
	if max <= 0 {
		return p
	}
	return &Retry{Provider: p, Max: max, sleep: sleepCtx}
}

func (r *Retry) Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error) {
	for attempt := 0; ; attempt++ {
		started := false
		resp, err := r.Provider.Stream(ctx, req, func(e Event) {
			if e.Kind != EventRetry {
				started = true
			}
			emit(onEvent, e)
		})
		if err == nil || started || attempt >= r.Max || ctx.Err() != nil || !retryable(err) {
			return resp, err
		}
		d := delay(err, attempt)
		emit(onEvent, Event{Kind: EventRetry,
			Text: fmt.Sprintf("%s, retrying in %s (%d/%d)", reason(err), seconds(d), attempt+1, r.Max)})
		if err := r.sleep(ctx, d); err != nil {
			return Response{}, err
		}
	}
}

// retryable reports whether err may pass if the call is made again.
func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Kind {
		case ErrRateLimited, ErrOverloaded:
			return true
		case ErrContextOverflow, ErrAuth:
			return false
		}
		// An error inside a stream with no known kind, such as Anthropic's
		// api_error, is the server's trouble too.
		return ae.Status == 0 || ae.Status >= 500 || ae.Status == 408
	}
	var ne net.Error
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.As(err, &ne)
}

// delay is how long to wait before retry attempt+1: what the API asked
// for, else the backoff.
func delay(err error, attempt int) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return ae.RetryAfter
	}
	d := baseDelay << attempt
	if d > maxDelay || d <= 0 {
		d = maxDelay
	}
	jitter := time.Duration((rand.Float64()*0.4 - 0.2) * float64(d))
	return d + jitter
}

func reason(err error) string {
	switch {
	case errors.Is(err, ErrRateLimited):
		return "Rate limited"
	case errors.Is(err, ErrOverloaded):
		return "The API is overloaded"
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return "The API failed"
	}
	return "The connection failed"
}

// seconds rounds d for display: "8s".
func seconds(d time.Duration) string {
	if d < time.Second {
		return "1s"
	}
	return (d + time.Second/2).Truncate(time.Second).String()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
