/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxResponseBytes int64 = 1 << 20
	defaultReconnectDelay         = 100 * time.Millisecond
	maxErrorBodyBytes             = 4096
	eventScannerBuffer            = 4096
)

// HTTPConfig configures the Streamable HTTP client transport.
type HTTPConfig struct {
	Client           *stdhttp.Client
	Headers          stdhttp.Header
	MaxResponseBytes int64
	ReconnectDelay   time.Duration
}

// HTTPTransport implements the MCP Streamable HTTP transport, including the
// GET event stream used for server notifications and server-initiated calls.
type HTTPTransport struct {
	endpoint string
	config   HTTPConfig

	mu            sync.Mutex
	started       bool
	closed        bool
	cancel        context.CancelFunc
	ctx           context.Context
	receiver      Receiver
	sessionID     string
	lastEventID   string
	eventsStarted bool
	closeOnce     sync.Once
	ready         chan struct{}
	readyOnce     sync.Once
	eventsErr     chan error
	eventsErrOnce sync.Once
}

// NewHTTPTransport creates a client for a Streamable HTTP MCP endpoint.
func NewHTTPTransport(endpoint string, config HTTPConfig) (*HTTPTransport, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("mcp/client: endpoint must be an http or https URL")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("mcp/client: endpoint must not contain a fragment")
	}
	if config.Client == nil {
		config.Client = stdhttp.DefaultClient
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.ReconnectDelay <= 0 {
		config.ReconnectDelay = defaultReconnectDelay
	}
	config.Headers = config.Headers.Clone()
	return &HTTPTransport{endpoint: parsed.String(), config: config, ready: make(chan struct{}), eventsErr: make(chan error, 1)}, nil
}

// Ready returns a channel closed when Start has registered its receiver.
func (transport *HTTPTransport) Ready() <-chan struct{} { return transport.ready }

// Start registers the receiver and waits until the parent context is done.
// The event stream is opened after the initialize response supplies a session.
func (transport *HTTPTransport) Start(ctx context.Context, receiver Receiver) error { //nolint:contextcheck // transport lifetime is owned by caller context
	if transport == nil {
		return ErrClosed
	}
	if receiver == nil {
		return errors.New("mcp/client: nil http receiver")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transport.mu.Lock()
	if transport.closed {
		transport.readyOnce.Do(func() { close(transport.ready) })
		transport.mu.Unlock()
		return ErrClosed
	}
	if transport.started {
		transport.mu.Unlock()
		return ErrStarted
	}
	transport.started = true
	transport.ctx, transport.cancel = context.WithCancel(ctx)
	transport.receiver = receiver
	transport.readyOnce.Do(func() { close(transport.ready) })
	transport.mu.Unlock()
	select {
	case <-transport.ctx.Done():
		return transport.ctx.Err()
	case err := <-transport.eventsErr:
		return err
	}
}

// Send posts one JSON-RPC message. A non-empty response body is returned for
// request/response POSTs; asynchronous events are delivered through Receiver.
func (transport *HTTPTransport) Send(ctx context.Context, payload []byte) ([]byte, error) { //nolint:contextcheck // request context is intentionally caller-owned
	if transport == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return nil, ErrClosed
	}
	if !transport.started {
		transport.mu.Unlock()
		return nil, ErrNotStarted
	}
	sessionID := transport.sessionID
	transport.mu.Unlock()
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, transport.endpoint, bytes.NewReader(payload)) //nolint:gosec // endpoint is validated by the constructor
	if err != nil {
		return nil, err
	}
	transport.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := transport.config.Client.Do(req) //nolint:bodyclose,gosec // the SSE branch transfers body ownership to readResponseEvents
	if err != nil {
		return nil, err
	}
	mediaType, _, mediaTypeErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaTypeErr == nil && mediaType == "text/event-stream" {
		requestID := responseID(payload)
		streamCtx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			_ = transport.readResponseEvents(streamCtx, resp.Body, requestID)
		}()
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, responseLimit(transport.config.MaxResponseBytes)))
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(body)) > transport.config.MaxResponseBytes {
		return nil, fmt.Errorf("mcp/client: http response exceeds %d bytes", transport.config.MaxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.JSONRPC == rpcVersion {
			return body, nil
		}
		return nil, fmt.Errorf("mcp/client: http status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if sessionID == "" {
		if discovered := resp.Header.Get("Mcp-Session-Id"); discovered != "" {
			transport.mu.Lock()
			if transport.sessionID == "" {
				transport.sessionID = discovered
			}
			transport.mu.Unlock()
			transport.startEvents()
		}
	}
	return body, nil
}

func responseID(payload []byte) json.RawMessage {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return nil
	}
	return envelope.ID
}

func (transport *HTTPTransport) readResponseEvents(ctx context.Context, body io.ReadCloser, requestID json.RawMessage) error {
	defer func() { _ = body.Close() }()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, eventScannerBuffer), scannerLimit(transport.config.MaxResponseBytes))
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := []byte(data.String())
		data.Reset()
		transport.mu.Lock()
		receiver := transport.receiver
		transport.mu.Unlock()
		if receiver == nil {
			return nil
		}
		if err := receiver(ctx, payload); err != nil {
			return err
		}
		if sameResponseID(payload, requestID) {
			return context.Canceled
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if data.Len() > 0 {
				_ = data.WriteByte('\n')
			}
			_, _ = data.WriteString(value)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scanner.Err()
}

func sameResponseID(payload []byte, requestID json.RawMessage) bool {
	if len(requestID) == 0 {
		return false
	}
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	return json.Unmarshal(payload, &envelope) == nil && bytes.Equal(envelope.ID, requestID)
}

func (transport *HTTPTransport) applyHeaders(req *stdhttp.Request) {
	for key, values := range transport.config.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

func (transport *HTTPTransport) startEvents() {
	transport.mu.Lock()
	if transport.eventsStarted || transport.ctx == nil || transport.sessionID == "" {
		transport.mu.Unlock()
		return
	}
	transport.eventsStarted = true
	ctx := transport.ctx
	transport.mu.Unlock()
	go transport.eventsLoop(ctx)
}

func (transport *HTTPTransport) eventsLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := transport.readEvents(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			var permanent eventStreamError
			if errors.As(err, &permanent) {
				transport.eventsErrOnce.Do(func() { transport.eventsErr <- err })
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(transport.config.ReconnectDelay):
			}
		}
	}
}

func (transport *HTTPTransport) readEvents(ctx context.Context) error {
	transport.mu.Lock()
	sessionID, lastID := transport.sessionID, transport.lastEventID
	transport.mu.Unlock()
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, transport.endpoint, nil)
	if err != nil {
		return err
	}
	transport.applyHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := transport.config.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != stdhttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return eventStreamError{message: fmt.Sprintf("mcp/client: event stream status %s: %s", resp.Status, strings.TrimSpace(string(body)))}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, eventScannerBuffer), scannerLimit(transport.config.MaxResponseBytes))
	var data strings.Builder
	var eventID string
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		transport.mu.Lock()
		transport.lastEventID = eventID
		receiver := transport.receiver
		transport.mu.Unlock()
		payload := []byte(data.String())
		data.Reset()
		if receiver == nil {
			return nil
		}
		return receiver(ctx, payload)
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if data.Len() > 0 {
				_ = data.WriteByte('\n')
			}
			_, _ = data.WriteString(value)
		case strings.HasPrefix(line, "id:"):
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return eventStreamError{message: fmt.Sprintf("mcp/client: read event stream: %v", err)}
	}
	return ErrTransportClosed
}

type eventStreamError struct{ message string }

func (err eventStreamError) Error() string { return err.message }

// Close cancels the event stream and future sends.
func (transport *HTTPTransport) Close() error {
	if transport == nil {
		return nil
	}
	transport.closeOnce.Do(func() {
		transport.mu.Lock()
		transport.closed = true
		if transport.cancel != nil {
			transport.cancel()
		}
		transport.mu.Unlock()
		transport.readyOnce.Do(func() { close(transport.ready) })
	})
	return nil
}
