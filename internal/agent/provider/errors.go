// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kinds of API failure. Test for them with errors.Is; the error itself is
// an *APIError that keeps the API's own message.
var (
	ErrRateLimited     = errors.New("rate limited")          // 429
	ErrOverloaded      = errors.New("overloaded")            // 529, 503
	ErrContextOverflow = errors.New("context overflow")      // the prompt does not fit the model
	ErrAuth            = errors.New("authentication failed") // 401, 403
)

// errEarlyEnd is a stream that stopped before the response was complete:
// the connection dropped.
var errEarlyEnd = fmt.Errorf("the response stream ended early: %w", io.ErrUnexpectedEOF)

// APIError is a failed call the API answered.
type APIError struct {
	// Kind is one of the Err* values above, or nil for any other failure.
	Kind   error
	Status int // HTTP status; 0 for an error sent inside the stream
	// RetryAfter is how long the API asked to wait; 0 when it did not say.
	RetryAfter time.Duration
	Msg        string
}

func (e *APIError) Error() string { return e.Msg }
func (e *APIError) Unwrap() error { return e.Kind }

// overflowPhrases are how APIs say the prompt is too long: Anthropic,
// OpenAI, OpenRouter, Gemini, Ollama, llama.cpp and vLLM.
var overflowPhrases = []string{
	"prompt is too long",
	"context_length_exceeded",
	"maximum context length",
	"context window",
	"context length",
	"too many tokens",
	"exceeds the available context",
	"exceeds the maximum number of tokens", // Gemini
}

// classify builds the error for a failed call from its status, headers and
// message. errType is the API's own error type or code when it sends one,
// such as Anthropic's "overloaded_error" or OpenAI's "rate_limit_exceeded".
func classify(status int, header http.Header, errType, msg string) *APIError {
	e := &APIError{Status: status, Msg: msg, RetryAfter: retryAfter(header)}
	low := strings.ToLower(msg)
	for _, p := range overflowPhrases {
		if strings.Contains(low, p) || errType == "context_length_exceeded" {
			e.Kind = ErrContextOverflow
			return e
		}
	}
	switch {
	case errType == "rate_limit_error" || errType == "rate_limit_exceeded" || status == http.StatusTooManyRequests:
		e.Kind = ErrRateLimited
	case errType == "overloaded_error" || status == 529 || status == http.StatusServiceUnavailable:
		e.Kind = ErrOverloaded
	case errType == "authentication_error" || errType == "permission_error" ||
		status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Kind = ErrAuth
	}
	return e
}

// retryAfter reads Retry-After (seconds or an HTTP date) or Anthropic's
// retry-after-ms.
func retryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	if ms, err := strconv.ParseFloat(h.Get("Retry-After-Ms"), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil && s > 0 {
		return time.Duration(s * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
