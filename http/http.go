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
	"io"
	stdhttp "net/http"
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
	sessionIDBytes            = 24
	shutdownTimeout           = 5 * time.Second
)

// Config configures a Streamable HTTP handler.
type Config struct {
	Address      string
	Path         string
	MaxBodyBytes int64
	SessionTTL   time.Duration
	MaxSessions  int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
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
	sessions map[string]time.Time
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
	return &Handler{server: server, config: config, sessions: map[string]time.Time{}}, nil
}

// ServeHTTP implements net/http.Handler.
func (handler *Handler) ServeHTTP(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
	if request.URL.Path != handler.config.Path {
		stdhttp.NotFound(writer, request)
		return
	}
	if request.Method != stdhttp.MethodPost {
		writer.Header().Set("Allow", stdhttp.MethodPost)
		stdhttp.Error(writer, "method not allowed", stdhttp.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		stdhttp.Error(writer, "unsupported media type", stdhttp.StatusUnsupportedMediaType)
		return
	}
	payload, err := io.ReadAll(stdhttp.MaxBytesReader(writer, request.Body, handler.config.MaxBodyBytes))
	if err != nil {
		stdhttp.Error(writer, "request body too large", stdhttp.StatusRequestEntityTooLarge)
		return
	}
	sessionID := request.Header.Get("Mcp-Session-Id")
	if sessionID != "" && !handler.validSession(sessionID) {
		stdhttp.Error(writer, "session not found", stdhttp.StatusNotFound)
		return
	}
	meta := mcp.RequestMeta{Transport: "http", Headers: headers(request.Header), SessionID: sessionID}
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

func (handler *Handler) validSession(id string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	_, ok := handler.sessions[id]
	return ok
}

func (handler *Handler) newSession() (string, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	if len(handler.sessions) >= handler.config.MaxSessions {
		return "", errors.New("session limit")
	}
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	handler.sessions[id] = time.Now().Add(handler.config.SessionTTL)
	return id, nil
}

func (handler *Handler) expire() {
	now := time.Now()
	for id, expiry := range handler.sessions {
		if now.After(expiry) {
			delete(handler.sessions, id)
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
