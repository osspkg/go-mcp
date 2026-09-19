/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package http provides the MCP Streamable HTTP transport.
package http

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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/sse"
)

const (
	defaultMaxBodyBytes int64 = 1 << 20
	defaultSessionTTL         = 30 * time.Minute
	defaultReadTimeout        = 15 * time.Second
	defaultWriteTimeout       = 30 * time.Second
	defaultIdleTimeout        = 60 * time.Second
	defaultMaxSessions        = 256
	streamEventLimit          = 128
	sessionIDBytes            = 24
	shutdownTimeout           = 5 * time.Second
)

// Config configures a Streamable HTTP handler.
type Config struct {
	Address        string
	Path           string
	MaxBodyBytes   int64
	SessionTTL     time.Duration
	MaxSessions    int
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	AllowedOrigins []string
}

// DefaultConfig returns transport defaults.
func DefaultConfig() Config {
	return Config{Address: ":8080", Path: "/mcp", MaxBodyBytes: defaultMaxBodyBytes, SessionTTL: defaultSessionTTL, MaxSessions: defaultMaxSessions, ReadTimeout: defaultReadTimeout, WriteTimeout: defaultWriteTimeout, IdleTimeout: defaultIdleTimeout}
}

// Transport adapts Streamable HTTP to mcp.Transport.
type Transport struct {
	Config    Config
	LegacySSE *sse.Config
}

// NewTransport creates an HTTP transport.
func NewTransport(config Config) *Transport { return &Transport{Config: config} }

// NewTransportWithSSE creates one HTTP listener serving Streamable HTTP and
// the legacy SSE endpoints together.
func NewTransportWithSSE(config Config, legacy sse.Config) *Transport {
	return &Transport{Config: config, LegacySSE: &legacy}
}

// Serve implements mcp.Transport.
func (transport *Transport) Serve(ctx context.Context, server *mcp.Server) error {
	if transport == nil {
		return errors.New("mcp/http: nil transport")
	}
	if ctx == nil {
		return errors.New("mcp/http: nil context")
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
	if config.Path == "" {
		config.Path = defaults.Path
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
	mux := stdhttp.NewServeMux()
	mux.Handle(config.Path, handler)
	var legacy *sse.Handler
	if transport.LegacySSE != nil {
		legacyConfig := *transport.LegacySSE
		legacyDefaults := sse.DefaultConfig()
		if legacyConfig.SSEPath == "" {
			legacyConfig.SSEPath = legacyDefaults.SSEPath
		}
		if legacyConfig.MessagePath == "" {
			legacyConfig.MessagePath = legacyDefaults.MessagePath
		}
		var legacyErr error
		legacy, legacyErr = sse.NewHandler(server, legacyConfig)
		if legacyErr != nil {
			return legacyErr
		}
		mux.Handle(legacyConfig.SSEPath, legacy)
		mux.Handle(legacyConfig.MessagePath, legacy)
	}
	if legacy != nil {
		defer legacy.Close()
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := Server(serveCtx, ServerConfig{Address: config.Address, Handler: mux, ReadTimeout: config.ReadTimeout, WriteTimeout: config.WriteTimeout, IdleTimeout: config.IdleTimeout})
	err = httpServer.ListenAndServe()
	if errors.Is(err, stdhttp.ErrServerClosed) {
		return serveCtx.Err()
	}
	return err
}

// Handler serves Streamable HTTP requests at Config.Path.
type Handler struct {
	server   *mcp.Server
	config   Config
	mu       sync.Mutex
	sessions map[string]*httpSession
	closed   bool
}

type streamEvent struct {
	id   string
	data []byte
}

type httpSession struct {
	expiry  time.Time
	next    uint64
	events  []streamEvent
	wake    chan struct{}
	done    chan struct{}
	pending map[string]chan []byte
}

// NewHandler creates a Streamable HTTP handler.
func NewHandler(server *mcp.Server, config Config) (*Handler, error) {
	if server == nil {
		return nil, errors.New("mcp/http: nil server")
	}
	if config.Path == "" {
		config.Path = "/mcp"
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultMaxBodyBytes
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = defaultSessionTTL
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = defaultMaxSessions
	}
	config.AllowedOrigins = slices.Clone(config.AllowedOrigins)
	return &Handler{server: server, config: config, sessions: map[string]*httpSession{}}, nil
}

// Close terminates all Streamable HTTP sessions and releases their peer
// callbacks. It is safe to call more than once.
func (handler *Handler) Close() {
	if handler == nil {
		return
	}
	handler.mu.Lock()
	if handler.closed {
		handler.mu.Unlock()
		return
	}
	handler.closed = true
	for id, session := range handler.sessions {
		delete(handler.sessions, id)
		close(session.done)
		handler.server.UnregisterPeer(id)
	}
	handler.mu.Unlock()
}

// ServeHTTP implements net/http.Handler.
func (handler *Handler) ServeHTTP(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	if request.URL.Path != handler.config.Path {
		stdhttp.NotFound(writer, request)
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" && !slices.Contains(handler.config.AllowedOrigins, origin) {
		stdhttp.Error(writer, "forbidden origin", stdhttp.StatusForbidden)
		return
	}
	if request.Method == stdhttp.MethodGet {
		if accept := request.Header.Get("Accept"); accept != "" && !strings.Contains(accept, "text/event-stream") {
			stdhttp.Error(writer, "client must accept text/event-stream", stdhttp.StatusNotAcceptable)
			return
		}
		handler.serveGET(writer, request)
		return
	}
	if request.Method != stdhttp.MethodPost {
		writer.Header().Set("Allow", "GET, POST")
		stdhttp.Error(writer, "method not allowed", stdhttp.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		stdhttp.Error(writer, "unsupported media type", stdhttp.StatusUnsupportedMediaType)
		return
	}
	payload, err := io.ReadAll(stdhttp.MaxBytesReader(writer, request.Body, handler.config.MaxBodyBytes))
	if err != nil {
		stdhttp.Error(writer, "request body too large", stdhttp.StatusRequestEntityTooLarge)
		return
	}
	sessionID := request.Header.Get("Mcp-Session-Id")
	if sessionID == "" && !isInitialize(payload) {
		stdhttp.Error(writer, "Mcp-Session-Id is required", stdhttp.StatusBadRequest)
		return
	}
	if sessionID != "" && !handler.validSession(sessionID) {
		stdhttp.Error(writer, "session not found", stdhttp.StatusNotFound)
		return
	}
	if sessionID != "" && isRPCResponse(payload) {
		if !handler.resolveResponse(sessionID, payload) {
			stdhttp.Error(writer, "unknown request id", stdhttp.StatusNotFound)
			return
		}
		writer.WriteHeader(stdhttp.StatusAccepted)
		return
	}
	meta := mcp.RequestMeta{Transport: "http", Headers: headers(request.Header), SessionID: sessionID}
	if sessionID != "" {
		meta.Notify = handler.sendNotification(sessionID)
		meta.Call = handler.callClient(sessionID)
	}
	response, err := handler.server.ServeJSON(request.Context(), payload, meta)
	if errors.Is(err, mcp.ErrUnauthorized) || bytes.Contains(response, []byte(`"code":-32001`)) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(stdhttp.StatusUnauthorized)
		_, _ = writer.Write(response)
		return
	}
	if errors.Is(err, mcp.ErrForbidden) || bytes.Contains(response, []byte(`"code":-32003`)) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(stdhttp.StatusForbidden)
		_, _ = writer.Write(response)
		return
	}
	if err != nil {
		stdhttp.Error(writer, "internal error", stdhttp.StatusInternalServerError)
		return
	}
	if sessionID == "" && isInitialize(payload) {
		sessionID, err = handler.newSession()
		if err != nil {
			stdhttp.Error(writer, "session capacity reached", stdhttp.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Mcp-Session-Id", sessionID)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(stdhttp.StatusOK)
	if response != nil {
		_, _ = writer.Write(response)
	}
}

func (handler *Handler) serveGET(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	sessionID := request.Header.Get("Mcp-Session-Id")
	if sessionID == "" || !handler.validSession(sessionID) {
		stdhttp.Error(writer, "session not found", stdhttp.StatusNotFound)
		return
	}
	flusher, ok := writer.(stdhttp.Flusher)
	if !ok {
		stdhttp.Error(writer, "streaming unsupported", stdhttp.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	lastID := request.Header.Get("Last-Event-ID")
	for {
		snapshot := handler.eventsAfter(sessionID, lastID)
		if !snapshot.ok {
			return
		}
		if len(snapshot.events) > 0 {
			writeEvents(writer, snapshot.events)
			flusher.Flush()
			return
		}
		if !waitForGET(request.Context(), snapshot) {
			return
		}
	}
}

func writeEvents(writer stdhttp.ResponseWriter, events []streamEvent) {
	for _, event := range events {
		_, _ = fmt.Fprintf(writer, "event: message\nid: %s\ndata: %s\n\n", event.id, event.data)
	}
}

func waitForGET(ctx context.Context, snapshot eventSnapshot) bool {
	timer := time.NewTimer(time.Until(snapshot.expiry))
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case <-snapshot.done:
		return false
	case <-snapshot.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (handler *Handler) validSession(id string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.closed {
		return false
	}
	handler.expire()
	_, ok := handler.sessions[id]
	return ok
}

func (handler *Handler) newSession() (string, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.closed {
		return "", errors.New("handler closed")
	}
	handler.expire()
	if len(handler.sessions) >= handler.config.MaxSessions {
		return "", errors.New("session limit")
	}
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	handler.sessions[id] = &httpSession{expiry: time.Now().Add(handler.config.SessionTTL), wake: make(chan struct{}), done: make(chan struct{}), pending: map[string]chan []byte{}}
	return id, nil
}

func (handler *Handler) expire() {
	now := time.Now()
	for id, session := range handler.sessions {
		if now.After(session.expiry) {
			delete(handler.sessions, id)
			close(session.done)
			handler.server.UnregisterPeer(id)
		}
	}
}

type eventSnapshot struct {
	events []streamEvent
	wake   <-chan struct{}
	done   <-chan struct{}
	expiry time.Time
	ok     bool
}

func (handler *Handler) eventsAfter(sessionID, lastID string) eventSnapshot {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	session, ok := handler.sessions[sessionID]
	if !ok {
		return eventSnapshot{}
	}
	handler.expire()
	if _, ok := handler.sessions[sessionID]; !ok {
		return eventSnapshot{}
	}
	start := -1
	for index, event := range session.events {
		if event.id == lastID {
			start = index
			break
		}
	}
	if lastID != "" && start < 0 {
		// A client may resume from an event no longer retained. Returning the
		// oldest retained events is the safest bounded-history fallback.
		start = -1
	}
	if start >= 0 {
		start++
	}
	if start < 0 {
		start = 0
	}
	result := append([]streamEvent(nil), session.events[start:]...)
	return eventSnapshot{events: result, wake: session.wake, done: session.done, expiry: session.expiry, ok: true}
}

func (handler *Handler) enqueue(sessionID string, payload []byte) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	session, ok := handler.sessions[sessionID]
	if !ok {
		return errors.New("mcp/http: session not found")
	}
	session.next++
	session.events = append(session.events, streamEvent{id: sessionID + ":" + strconv.FormatUint(session.next, 10), data: append([]byte(nil), payload...)})
	if len(session.events) > streamEventLimit {
		session.events = session.events[len(session.events)-streamEventLimit:]
	}
	close(session.wake)
	session.wake = make(chan struct{})
	session.expiry = time.Now().Add(handler.config.SessionTTL)
	return nil
}

func (handler *Handler) sendNotification(sessionID string) mcp.NotificationSender {
	return func(ctx context.Context, method string, params any) error { //nolint:contextcheck // callback forwards its caller context
		if ctx == nil {
			ctx = context.Background() //nolint:contextcheck // a nil caller context has no parent
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
		return handler.enqueue(sessionID, payload)
	}
}

func (handler *Handler) callClient(sessionID string) mcp.ClientCaller {
	return func(ctx context.Context, method string, params any) (json.RawMessage, error) { //nolint:contextcheck // callback forwards its caller context
		if ctx == nil {
			ctx = context.Background() //nolint:contextcheck // a nil caller context has no parent
		}
		handler.mu.Lock()
		session, ok := handler.sessions[sessionID]
		if !ok {
			handler.mu.Unlock()
			return nil, errors.New("mcp/http: session not found")
		}
		session.next++
		id := sessionID + ":req:" + strconv.FormatUint(session.next, 10)
		waiter := make(chan []byte, 1)
		session.pending[id] = waiter
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err == nil {
			session.events = append(session.events, streamEvent{id: sessionID + ":" + strconv.FormatUint(session.next, 10), data: payload})
			if len(session.events) > streamEventLimit {
				session.events = session.events[len(session.events)-streamEventLimit:]
			}
			close(session.wake)
			session.wake = make(chan struct{})
		}
		handler.mu.Unlock()
		if err != nil {
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
			handler.mu.Lock()
			delete(session.pending, id)
			handler.mu.Unlock()
			return nil, ctx.Err()
		case <-session.done:
			return nil, errors.New("mcp/http: session closed")
		}
	}
}

func (handler *Handler) resolveResponse(sessionID string, payload []byte) bool {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) == 0 {
		return false
	}
	var id string
	if json.Unmarshal(envelope.ID, &id) != nil || id == "" {
		return false
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	session, ok := handler.sessions[sessionID]
	if !ok {
		return false
	}
	waiter, ok := session.pending[id]
	if !ok {
		return false
	}
	delete(session.pending, id)
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

func headers(input stdhttp.Header) map[string]string {
	result := make(map[string]string, len(input))
	for key, values := range input {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

func isInitialize(payload []byte) bool {
	var request struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(payload, &request) == nil && request.Method == "initialize"
}

// ServerConfig configures an HTTP server and graceful shutdown.
type ServerConfig struct {
	Address      string
	Handler      stdhttp.Handler
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// Server returns an HTTP server that shuts down when a non-nil ctx is cancelled.
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
