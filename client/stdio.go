/*
 * Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 * Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package client

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
)

const (
	defaultMaxMessageBytes = 1 << 20
	stdioScannerBuffer     = 64 * 1024
)

// StdioConfig configures the newline-delimited JSON-RPC client transport.
type StdioConfig struct {
	MaxMessageBytes int
}

// StdioTransport communicates with an MCP server over stdin/stdout-like
// streams. Close interrupts a closable input stream; the output writer remains
// owned by the caller and is never closed.
type StdioTransport struct {
	input  io.Reader
	output io.Writer
	config StdioConfig

	mu        sync.Mutex
	started   bool
	closed    bool
	cancel    context.CancelFunc
	close     sync.Once
	ready     chan struct{}
	readyOnce sync.Once
}

// NewStdioTransport creates a stdio transport over input and output.
func NewStdioTransport(input io.Reader, output io.Writer, config StdioConfig) (*StdioTransport, error) {
	if input == nil || output == nil {
		return nil, errors.New("mcp/client: stdio input and output are required")
	}
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = defaultMaxMessageBytes
	}
	return &StdioTransport{input: input, output: output, config: config, ready: make(chan struct{})}, nil
}

// Ready returns a channel closed when Start has installed the stdio reader.
func (transport *StdioTransport) Ready() <-chan struct{} { return transport.ready }

// Start reads newline-delimited JSON-RPC messages until EOF or cancellation.
func (transport *StdioTransport) Start(ctx context.Context, receiver Receiver) error { //nolint:contextcheck // transport lifetime is owned by caller context
	if transport == nil {
		return ErrClosed
	}
	if receiver == nil {
		return errors.New("mcp/client: nil stdio receiver")
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
	readCtx, cancel := context.WithCancel(ctx)
	transport.cancel = cancel
	transport.readyOnce.Do(func() { close(transport.ready) })
	transport.mu.Unlock()
	defer cancel()

	scanner := bufio.NewScanner(transport.input)
	buffer := make([]byte, stdioScannerBuffer)
	scanner.Buffer(buffer, transport.config.MaxMessageBytes)
	for scanner.Scan() {
		select {
		case <-readCtx.Done():
			return readCtx.Err()
		default:
		}
		payload := append([]byte(nil), scanner.Bytes()...)
		if len(payload) == 0 {
			continue
		}
		if err := receiver(readCtx, payload); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return ErrTransportClosed
}

// Send writes one JSON-RPC message followed by a newline.
func (transport *StdioTransport) Send(ctx context.Context, payload []byte) ([]byte, error) { //nolint:contextcheck // request context is intentionally caller-owned
	if transport == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.closed {
		return nil, ErrClosed
	}
	if transport.cancel == nil {
		return nil, ErrNotStarted
	}
	line := make([]byte, len(payload)+1)
	copy(line, payload)
	line[len(payload)] = '\n'
	if _, err := transport.output.Write(line); err != nil {
		return nil, err
	}
	return nil, nil
}

// Close stops the reader and closes closable streams.
func (transport *StdioTransport) Close() error {
	if transport == nil {
		return nil
	}
	transport.close.Do(func() {
		transport.mu.Lock()
		transport.closed = true
		if transport.cancel != nil {
			transport.cancel()
		}
		transport.mu.Unlock()
		transport.readyOnce.Do(func() { close(transport.ready) })
		if closer, ok := transport.input.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	return nil
}
