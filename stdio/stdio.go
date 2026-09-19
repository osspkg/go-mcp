/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package stdio serves newline-delimited MCP JSON-RPC over standard streams.
package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"

	"go.osspkg.com/mcp"
)

const (
	initialScanBuffer = 1024
	maximumScanBuffer = 1 << 20
	stdioSessionID    = "stdio"
)

// Config configures stdio framing limits.
type Config struct {
	MaxMessageBytes int
}

// DefaultConfig returns safe stdio framing defaults.
func DefaultConfig() Config { return Config{MaxMessageBytes: maximumScanBuffer} }

// Transport adapts stdio streams to mcp.Transport.
type Transport struct {
	Input  io.Reader
	Output io.Writer
	Config Config
}

// NewTransport creates a stdio transport using the provided streams.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	return NewTransportWithConfig(input, output, DefaultConfig())
}

// NewTransportWithConfig creates a stdio transport with explicit framing limits.
func NewTransportWithConfig(input io.Reader, output io.Writer, config Config) *Transport {
	return &Transport{Input: input, Output: output, Config: config}
}

// Serve implements mcp.Transport.
func (transport *Transport) Serve(ctx context.Context, server *mcp.Server) error {
	if transport == nil {
		return errors.New("mcp/stdio: nil transport")
	}
	return ServeWithConfig(ctx, server, transport.Input, transport.Output, transport.Config)
}

// Serve reads requests from input and writes responses to output until the
// context is cancelled or input is closed.
func Serve(ctx context.Context, server *mcp.Server, input io.Reader, output io.Writer) error {
	return ServeWithConfig(ctx, server, input, output, DefaultConfig())
}

// ServeWithConfig serves stdio requests with explicit framing limits.
func ServeWithConfig(ctx context.Context, server *mcp.Server, input io.Reader, output io.Writer, config Config) error { //nolint:contextcheck,revive // nil context is normalized at the transport boundary
	if server == nil || input == nil || output == nil {
		return errors.New("mcp/stdio: server, input, and output are required")
	}
	if ctx == nil {
		ctx = context.Background() //nolint:contextcheck // nil transport context has no parent
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(input)
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = maximumScanBuffer
	}
	initialBuffer := initialScanBuffer
	if config.MaxMessageBytes < initialBuffer {
		initialBuffer = config.MaxMessageBytes
	}
	scanner.Buffer(make([]byte, initialBuffer), config.MaxMessageBytes)
	writer := bufio.NewWriter(output)
	defer func() { _ = writer.Flush() }()
	if closer, ok := input.(io.Closer); ok {
		go func() {
			<-serveCtx.Done()
			_ = closer.Close()
		}()
	}
	peer := newPeer(writer)
	defer server.UnregisterPeer(stdioSessionID)
	var handlers sync.WaitGroup
	var errMu sync.Mutex
	var handlerErr error
	recordError := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if handlerErr == nil {
			handlerErr = err
			cancel()
		}
		errMu.Unlock()
	}
	for scanner.Scan() {
		if err := serveCtx.Err(); err != nil {
			cancel()
			break
		}
		payload := append([]byte(nil), scanner.Bytes()...)
		if peer.resolve(payload) || isRPCResponse(payload) {
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			response, err := server.ServeJSON(serveCtx, payload, mcp.RequestMeta{Transport: "stdio", Headers: map[string]string{}, SessionID: stdioSessionID, Notify: peer.notify, Call: peer.call})
			if err != nil {
				recordError(err)
				return
			}
			if response == nil {
				return
			}
			if err := peer.write(response); err != nil {
				recordError(err)
			}
		}()
	}
	scanErr := scanner.Err()
	contextErr := serveCtx.Err()
	cancel()
	handlers.Wait()
	errMu.Lock()
	deferredErr := handlerErr
	errMu.Unlock()
	if deferredErr != nil {
		return deferredErr
	}
	if scanErr != nil {
		if contextErr != nil {
			return contextErr
		}
		return scanErr
	}
	if contextErr != nil {
		return contextErr
	}
	return nil
}

type peer struct {
	writer  *bufio.Writer
	writeMu sync.Mutex
	mu      sync.Mutex
	next    uint64
	pending map[string]chan []byte
}

func newPeer(writer *bufio.Writer) *peer {
	return &peer{writer: writer, pending: map[string]chan []byte{}}
}

func (peer *peer) write(payload []byte) error {
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	if _, err := peer.writer.Write(append(payload, '\n')); err != nil {
		return err
	}
	return peer.writer.Flush()
}

func (peer *peer) notify(ctx context.Context, method string, params any) error { //nolint:contextcheck // forwards caller context to the transport boundary
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return peer.write(payload)
}

func (peer *peer) call(ctx context.Context, method string, params any) (json.RawMessage, error) { //nolint:contextcheck // forwards caller context to the transport boundary
	if ctx == nil {
		ctx = context.Background()
	}
	peer.mu.Lock()
	peer.next++
	id := stdioSessionID + ":req:" + strconv.FormatUint(peer.next, 10)
	waiter := make(chan []byte, 1)
	peer.pending[id] = waiter
	peer.mu.Unlock()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		peer.mu.Lock()
		delete(peer.pending, id)
		peer.mu.Unlock()
		return nil, err
	}
	if err := peer.write(payload); err != nil {
		peer.mu.Lock()
		delete(peer.pending, id)
		peer.mu.Unlock()
		return nil, err
	}
	select {
	case response := <-waiter:
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil {
			return nil, err
		}
		if envelope.Error != nil {
			return nil, errors.New(envelope.Error.Message)
		}
		return envelope.Result, nil
	case <-ctx.Done():
		peer.mu.Lock()
		delete(peer.pending, id)
		peer.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (peer *peer) resolve(payload []byte) bool {
	var envelope struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Method != "" || len(envelope.ID) == 0 || (len(envelope.Result) == 0 && len(envelope.Error) == 0) {
		return false
	}
	var id string
	if json.Unmarshal(envelope.ID, &id) != nil || id == "" {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	waiter, ok := peer.pending[id]
	if !ok {
		return false
	}
	delete(peer.pending, id)
	waiter <- append([]byte(nil), payload...)
	return true
}

func isRPCResponse(payload []byte) bool {
	var envelope struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Method != "" || len(envelope.ID) == 0 {
		return false
	}
	return len(envelope.Result) > 0 || len(envelope.Error) > 0
}
