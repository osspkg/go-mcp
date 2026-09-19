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
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	sseMaxErrorBodyBytes = 4096
	sseScannerBuffer     = 4096
)

// SSEConfig configures the legacy HTTP+SSE client transport.
type SSEConfig struct {
	Client           *stdhttp.Client
	Headers          stdhttp.Header
	MaxResponseBytes int64
}

// SSETransport implements the legacy /sse and /message MCP endpoints.
type SSETransport struct {
	sseEndpoint string
	config      SSEConfig

	mu         sync.Mutex
	started    bool
	closed     bool
	cancel     context.CancelFunc
	ctx        context.Context
	receiver   Receiver
	messageURL string
	ready      chan struct{}
	readyOnce  sync.Once
	startReady chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
}

// NewSSETransport creates a client for a legacy SSE endpoint, for example
// http://localhost:8080/sse. The endpoint URL may be relative to no other URL.
func NewSSETransport(endpoint string, config SSEConfig) (*SSETransport, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("mcp/client: SSE endpoint must be an http or https URL")
	}
	if config.Client == nil {
		config.Client = stdhttp.DefaultClient
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	config.Headers = config.Headers.Clone()
	return &SSETransport{sseEndpoint: parsed.String(), config: config, ready: make(chan struct{}), startReady: make(chan struct{})}, nil
}

// Ready returns a channel closed when Start has registered its receiver.
func (transport *SSETransport) Ready() <-chan struct{} { return transport.startReady }

// Start opens the legacy SSE stream and blocks until it closes.
func (transport *SSETransport) Start(ctx context.Context, receiver Receiver) error { //nolint:contextcheck // transport lifetime is owned by caller context
	if transport == nil {
		return ErrClosed
	}
	if receiver == nil {
		return errors.New("mcp/client: nil SSE receiver")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transport.mu.Lock()
	if transport.closed {
		transport.startOnce.Do(func() { close(transport.startReady) })
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
	readCtx := transport.ctx
	transport.startOnce.Do(func() { close(transport.startReady) })
	transport.mu.Unlock()
	return transport.readStream(readCtx)
}

func (transport *SSETransport) readStream(ctx context.Context) error {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, transport.sseEndpoint, nil)
	if err != nil {
		return err
	}
	transport.applyHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := transport.config.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != stdhttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, sseMaxErrorBodyBytes))
		return fmt.Errorf("mcp/client: SSE status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, sseScannerBuffer), scannerLimit(transport.config.MaxResponseBytes))
	var event, data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event.Reset()
			return nil
		}
		payload := []byte(data.String())
		name := event.String()
		data.Reset()
		event.Reset()
		if name == "endpoint" {
			return transport.setMessageEndpoint(payload)
		}
		transport.mu.Lock()
		receiver := transport.receiver
		transport.mu.Unlock()
		if receiver != nil {
			return receiver(ctx, payload)
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
		case strings.HasPrefix(line, "event:"):
			_, _ = event.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
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
	if err := scanner.Err(); err != nil {
		return err
	}
	return ErrTransportClosed
}

func (transport *SSETransport) setMessageEndpoint(payload []byte) error {
	endpoint, err := url.Parse(string(payload))
	if err != nil {
		return err
	}
	base, err := url.Parse(transport.sseEndpoint)
	if err != nil {
		return err
	}
	messageURL := base.ResolveReference(endpoint)
	if !sameOrigin(base, messageURL) {
		return errors.New("mcp/client: SSE message endpoint must use the SSE endpoint origin")
	}
	transport.mu.Lock()
	transport.messageURL = messageURL.String()
	transport.mu.Unlock()
	transport.readyOnce.Do(func() { close(transport.ready) })
	return nil
}

func sameOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

// Send posts a JSON-RPC message to the session-specific message endpoint.
func (transport *SSETransport) Send(ctx context.Context, payload []byte) ([]byte, error) { //nolint:contextcheck // request context is intentionally caller-owned
	if transport == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-transport.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return nil, ErrClosed
	}
	messageURL := transport.messageURL
	transport.mu.Unlock()
	if messageURL == "" {
		return nil, errors.New("mcp/client: SSE endpoint did not advertise a message URL")
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, messageURL, bytes.NewReader(payload)) //nolint:gosec // message URL is resolved from the validated SSE endpoint
	if err != nil {
		return nil, err
	}
	transport.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.config.Client.Do(req) //nolint:gosec // message URL is resolved from the validated SSE endpoint
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit(transport.config.MaxResponseBytes)))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > transport.config.MaxResponseBytes {
		return nil, fmt.Errorf("mcp/client: SSE response exceeds %d bytes", transport.config.MaxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.JSONRPC == rpcVersion {
			return body, nil
		}
		return nil, fmt.Errorf("mcp/client: SSE message status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (transport *SSETransport) applyHeaders(req *stdhttp.Request) {
	for key, values := range transport.config.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

// Close cancels the SSE stream and future sends.
func (transport *SSETransport) Close() error {
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
		transport.startOnce.Do(func() { close(transport.startReady) })
		transport.readyOnce.Do(func() { close(transport.ready) })
	})
	return nil
}
