package notifier

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

type RetryOptions struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	QueueSize      int
}

type retryReporter struct {
	base Reporter
	opts RetryOptions
	q    chan Event
}

func NewRetryReporter(base Reporter, opts RetryOptions) Reporter {
	if base == nil {
		return nil
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = 500 * time.Millisecond
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 10 * time.Second
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1024
	}

	r := &retryReporter{
		base: base,
		opts: opts,
		q:    make(chan Event, opts.QueueSize),
	}
	go r.loop()
	return r
}

func (r *retryReporter) Target() string {
	return r.base.Target()
}

func (r *retryReporter) Report(ctx context.Context, event Event) error {
	select {
	case r.q <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("event queue is full")
	}
}

func (r *retryReporter) loop() {
	for event := range r.q {
		r.deliver(event)
	}
}

func (r *retryReporter) deliver(event Event) {
	var lastErr error
	for retry := 0; retry <= r.opts.MaxRetries; retry++ {
		err := r.base.Report(context.Background(), event)
		if err == nil {
			return
		}
		lastErr = err
		if !shouldRetry(err) || retry == r.opts.MaxRetries {
			break
		}
		delay := r.backoff(retry)
		time.Sleep(delay)
	}
	log.Printf(
		"event dropped after retries: type=%s event_id=%s retries=%d err=%v",
		event.Type,
		event.EventID,
		r.opts.MaxRetries,
		lastErr,
	)
}

func (r *retryReporter) backoff(retry int) time.Duration {
	delay := r.opts.InitialBackoff
	for i := 0; i < retry; i++ {
		delay *= 2
		if delay >= r.opts.MaxBackoff {
			return r.opts.MaxBackoff
		}
	}
	if delay > r.opts.MaxBackoff {
		return r.opts.MaxBackoff
	}
	return delay
}

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	var dErr *DeliveryError
	if errors.As(err, &dErr) {
		return dErr.Retryable
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return true
}
