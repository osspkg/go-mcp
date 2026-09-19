/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package sse implements the legacy MCP HTTP+SSE transport.
package sse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"go.osspkg.com/mcp"
)

const (
	defaultMaxBodyBytes    int64 = 1 << 20
	defaultSessionTTL            = 30 * time.Minute
	defaultReadTimeout           = 15 * time.Second
	defaultWriteTimeout          = 30 * time.Second
	defaultIdleTimeout           = 60 * time.Second
	defaultMaxSessions           = 256
	sessionIDBytes               = 24
	messageQueueSize             = 16
	cleanupIntervalDivisor       = 2
	maxCleanupInterval           = time.Minute
	minCleanupInterval           = 10 * time.Millisecond
	shutdownTimeout              = 5 * time.Second
)

// Config configures the legacy SSE endpoints.
type Config struct {
	Address      string
	SSEPath      string
	MessagePath  string
	MaxBodyBytes int64
	SessionTTL   time.Duration
	MaxSessions  int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// DefaultConfig returns legacy endpoint defaults.
func DefaultConfig() Config {
	return Config{Address: ":8080", SSEPath: "/sse", MessagePath: "/message", MaxBodyBytes: defaultMaxBodyBytes, SessionTTL: defaultSessionTTL, MaxSessions: defaultMaxSessions, ReadTimeout: defaultReadTimeout, WriteTimeout: defaultWriteTimeout, IdleTimeout: defaultIdleTimeout}
}

// Transport adapts legacy SSE to mcp.Transport.
type Transport struct{ Config Config }

// NewTransport creates a legacy SSE transport.
func NewTransport(config Config) *Transport { return &Transport{Config: config} }

// Serve implements mcp.Transport.
func (transport *Transport) Serve(ctx context.Context, server *mcp.Server) error {
	if transport == nil {
		return errors.New("mcp/sse: nil transport")
	}
	if ctx == nil {
		return errors.New("mcp/sse: nil context")
	}
	handler, err := NewHandler(server, transport.Config)
	if err != nil {
		return err
	}
	defer handler.Close()
	config := transport.Config
	defaults := DefaultConfig()
	if config.Address == "" {
		config.Address = defaults.Address
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = defaults.ReadTimeout
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = defaults.WriteTimeout
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = defaults.IdleTimeout
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := Server(serveCtx, ServerConfig{Address: config.Address, Handler: handler, ReadTimeout: config.ReadTimeout, WriteTimeout: config.WriteTimeout, IdleTimeout: config.IdleTimeout})
	err = httpServer.ListenAndServe()
	if errors.Is(err, stdhttp.ErrServerClosed) {
		return serveCtx.Err()
	}
	return err
}

type session struct {
	expiry   time.Time
	messages chan []byte
	done     chan struct{}
	next     uint64
	pending  map[string]chan []byte
}

// Handler serves a legacy SSE endpoint and its message endpoint.
type Handler struct {
	server         *mcp.Server
	config         Config
	mu             sync.Mutex
	sessions       map[string]*session
	cleanupRunning bool
	closed         bool
	shutdown       chan struct{}
	closeOnce      sync.Once
}

// NewHandler creates a legacy SSE transport handler.
func NewHandler(server *mcp.Server, config Config) (*Handler, error) {
	if server == nil {
		return nil, errors.New("mcp/sse: nil server")
	}
	defaults := DefaultConfig()
	if config.SSEPath == "" {
		config.SSEPath = defaults.SSEPath
	}
	if config.MessagePath == "" {
		config.MessagePath = defaults.MessagePath
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaults.MaxBodyBytes
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = defaults.SessionTTL
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = defaults.MaxSessions
	}
	return &Handler{server: server, config: config, sessions: map[string]*session{}, shutdown: make(chan struct{})}, nil
}

// Close terminates every active SSE session and stops background cleanup.
func (handler *Handler) Close() {
	if handler == nil {
		return
	}
	handler.closeOnce.Do(func() {
		close(handler.shutdown)
		handler.mu.Lock()
		handler.closed = true
		for id, item := range handler.sessions {
			delete(handler.sessions, id)
			close(item.done)
			handler.server.UnregisterPeer(id)
		}
		handler.cleanupRunning = false
		handler.mu.Unlock()
	})
}

// ServeHTTP implements net/http.Handler.
func (handler *Handler) ServeHTTP(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	switch request.URL.Path {
	case handler.config.SSEPath:
		handler.serveSSE(writer, request)
	case handler.config.MessagePath:
		handler.serveMessage(writer, request)
	default:
		stdhttp.NotFound(writer, request)
	}
}

func (handler *Handler) serveSSE(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	if request.Method != stdhttp.MethodGet {
		writer.Header().Set("Allow", stdhttp.MethodGet)
		stdhttp.Error(writer, "method not allowed", stdhttp.StatusMethodNotAllowed)
		return
	}
	flusher, ok := writer.(stdhttp.Flusher)
	if !ok {
		stdhttp.Error(writer, "streaming unsupported", stdhttp.StatusInternalServerError)
		return
	}
	id, item, err := handler.newSession()
	if err != nil {
		stdhttp.Error(writer, "session capacity reached", stdhttp.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	endpoint := handler.config.MessagePath + "?sessionId=" + url.QueryEscape(id)
	_, _ = fmt.Fprintf(writer, "event: endpoint\ndata: %s\n\n", endpoint)
	flusher.Flush()
	for {
		select {
		case <-request.Context().Done():
			handler.deleteSession(id)
			return
		case <-item.done:
			return
		case response := <-item.messages:
			_, _ = fmt.Fprintf(writer, "event: message\ndata: %s\n\n", response)
			flusher.Flush()
		}
	}
}

func (handler *Handler) serveMessage(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	if request.Method != stdhttp.MethodPost {
		writer.Header().Set("Allow", stdhttp.MethodPost)
		stdhttp.Error(writer, "method not allowed", stdhttp.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		stdhttp.Error(writer, "unsupported media type", stdhttp.StatusUnsupportedMediaType)
		return
	}
	id := request.URL.Query().Get("sessionId")
	item := handler.getSession(id)
	if item == nil {
		stdhttp.Error(writer, "session not found", stdhttp.StatusNotFound)
		return
	}
	payload, err := io.ReadAll(stdhttp.MaxBytesReader(writer, request.Body, handler.config.MaxBodyBytes))
	if err != nil {
		stdhttp.Error(writer, "request body too large", stdhttp.StatusRequestEntityTooLarge)
		return
	}
	if isRPCResponse(payload) {
		if !handler.resolveResponse(id, payload) {
			stdhttp.Error(writer, "unknown request id", stdhttp.StatusNotFound)
			return
		}
		writer.WriteHeader(stdhttp.StatusAccepted)
		return
	}
	meta := mcp.RequestMeta{Transport: "sse", Headers: headers(request.Header), SessionID: id, Notify: handler.sendNotification(id), Call: handler.callClient(id)}
	response, err := handler.server.ServeJSON(request.Context(), payload, meta)
	if bytes.Contains(response, []byte(`"code":-32001`)) {
		writer.WriteHeader(stdhttp.StatusUnauthorized)
		return
	}
	if bytes.Contains(response, []byte(`"code":-32003`)) {
		writer.WriteHeader(stdhttp.StatusForbidden)
		return
	}
	if err != nil {
		stdhttp.Error(writer, "internal error", stdhttp.StatusInternalServerError)
		return
	}
	if response != nil {
		select {
		case item.messages <- response:
		case <-item.done:
			stdhttp.Error(writer, "session expired", stdhttp.StatusNotFound)
			return
		case <-request.Context().Done():
			return
		}
	}
	writer.WriteHeader(stdhttp.StatusAccepted)
}

func (handler *Handler) newSession() (string, *session, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.closed {
		return "", nil, errors.New("handler closed")
	}
	handler.expireLocked()
	if len(handler.sessions) >= handler.config.MaxSessions {
		return "", nil, errors.New("session limit")
	}
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	id := hex.EncodeToString(raw)
	item := &session{
		expiry:   time.Now().Add(handler.config.SessionTTL),
		messages: make(chan []byte, messageQueueSize),
		done:     make(chan struct{}),
		pending:  map[string]chan []byte{},
	}
	handler.sessions[id] = item
	if !handler.cleanupRunning {
		handler.cleanupRunning = true
		go handler.cleanupLoop()
	}
	return id, item, nil
}

func (handler *Handler) sendNotification(id string) mcp.NotificationSender {
	return func(ctx context.Context, method string, params any) error { //nolint:contextcheck // callback forwards its caller context
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		if err != nil {
			return err
		}
		item := handler.getSession(id)
		if item == nil {
			return errors.New("mcp/sse: session not found")
		}
		if ctx == nil {
			ctx = context.Background() //nolint:contextcheck // a nil caller context has no parent
		}
		select {
		case item.messages <- payload:
			return nil
		case <-item.done:
			return errors.New("mcp/sse: session closed")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (handler *Handler) callClient(id string) mcp.ClientCaller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, error) { //nolint:contextcheck // callback forwards its caller context
		item := handler.getSession(id)
		if item == nil {
			return nil, errors.New("mcp/sse: session not found")
		}
		handler.mu.Lock()
		item.next++
		requestID := id + ":req:" + strconv.FormatUint(item.next, 10)
		waiter := make(chan []byte, 1)
		item.pending[requestID] = waiter
		handler.mu.Unlock()
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": requestID, "method": method, "params": params})
		if err != nil {
			handler.mu.Lock()
			delete(item.pending, requestID)
			handler.mu.Unlock()
			return nil, err
		}
		if ctx == nil {
			ctx = context.Background() //nolint:contextcheck // a nil caller context has no parent
		}
		select {
		case item.messages <- payload:
		case <-item.done:
			return nil, errors.New("mcp/sse: session closed")
		case <-ctx.Done():
			handler.mu.Lock()
			delete(item.pending, requestID)
			handler.mu.Unlock()
			return nil, ctx.Err()
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
		case <-item.done:
			return nil, errors.New("mcp/sse: session closed")
		case <-ctx.Done():
			handler.mu.Lock()
			delete(item.pending, requestID)
			handler.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (handler *Handler) resolveResponse(id string, payload []byte) bool {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) == 0 {
		return false
	}
	var requestID string
	if json.Unmarshal(envelope.ID, &requestID) != nil || requestID == "" {
		return false
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	item, ok := handler.sessions[id]
	if !ok {
		return false
	}
	waiter, ok := item.pending[requestID]
	if !ok {
		return false
	}
	delete(item.pending, requestID)
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

func (handler *Handler) getSession(id string) *session {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expireLocked()
	return handler.sessions[id]
}

func (handler *Handler) deleteSession(id string) {
	handler.mu.Lock()
	if item, ok := handler.sessions[id]; ok {
		delete(handler.sessions, id)
		if item.done != nil {
			close(item.done)
		}
		handler.server.UnregisterPeer(id)
	}
	handler.mu.Unlock()
}

func (handler *Handler) cleanupLoop() {
	ticker := time.NewTicker(handler.cleanupInterval())
	defer ticker.Stop()
	for {
		select {
		case <-handler.shutdown:
			return
		case <-ticker.C:
		}
		handler.mu.Lock()
		handler.expireLocked()
		if len(handler.sessions) == 0 {
			handler.cleanupRunning = false
			handler.mu.Unlock()
			return
		}
		handler.mu.Unlock()
	}
}

func (handler *Handler) cleanupInterval() time.Duration {
	interval := handler.config.SessionTTL / cleanupIntervalDivisor
	if interval < minCleanupInterval {
		return minCleanupInterval
	}
	if interval > maxCleanupInterval {
		return maxCleanupInterval
	}
	return interval
}

func (handler *Handler) expireLocked() {
	now := time.Now()
	for id, item := range handler.sessions {
		if now.After(item.expiry) {
			delete(handler.sessions, id)
			if item.done != nil {
				close(item.done)
			}
			handler.server.UnregisterPeer(id)
		}
	}
}

func headers(input stdhttp.Header) map[string]string {
	result := make(map[string]string, len(input))
	for key, values := range input {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

// ServerConfig configures an HTTP server and graceful shutdown.
type ServerConfig struct {
	Address      string
	Handler      stdhttp.Handler
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// Server creates an HTTP server that is shut down when a non-nil ctx is cancelled.
// A nil context leaves shutdown under the caller's control.
func Server(ctx context.Context, config ServerConfig) *stdhttp.Server {
	server := &stdhttp.Server{Addr: config.Address, Handler: config.Handler, ReadTimeout: config.ReadTimeout, WriteTimeout: config.WriteTimeout, IdleTimeout: config.IdleTimeout}
	if ctx == nil {
		return server
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	return server
}
