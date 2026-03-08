package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Event struct {
	EventID        string         `json:"event_id,omitempty"`
	SchemaVersion  string         `json:"schema_version,omitempty"`
	SessionSeq     int64          `json:"session_seq,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Type           string         `json:"type"`
	Severity       string         `json:"severity"`
	Timestamp      string         `json:"timestamp"`
	Payload        map[string]any `json:"payload"`
}

type Reporter interface {
	Report(ctx context.Context, event Event) error
	Target() string
}

type HTTPReporter struct {
	target string
	client *http.Client
}

type DeliveryError struct {
	StatusCode int
	Retryable  bool
	Message    string
	Cause      error
}

func (e *DeliveryError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "delivery failed"
}

func (e *DeliveryError) Unwrap() error {
	return e.Cause
}

func NewHTTPReporter(target string) *HTTPReporter {
	return &HTTPReporter{
		target: target,
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

func (h *HTTPReporter) Target() string {
	return h.target
}

func (h *HTTPReporter) Report(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		retryable := true
		if errors.Is(err, context.Canceled) {
			retryable = false
		}
		return &DeliveryError{
			Retryable: retryable,
			Message:   fmt.Sprintf("send event: %v", err),
			Cause:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		bodyText := strings.TrimSpace(string(raw))
		msg := fmt.Sprintf("session manager returned status %d", resp.StatusCode)
		if bodyText != "" {
			msg = msg + ": " + bodyText
		}
		return &DeliveryError{
			StatusCode: resp.StatusCode,
			Retryable:  resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
			Message:    msg,
		}
	}

	return nil
}

func BuildReporter(target string, retryOpts RetryOptions) Reporter {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	base := NewHTTPReporter(target)
	return NewRetryReporter(base, retryOpts)
}
