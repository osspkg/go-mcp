// Package sse implements the legacy MCP HTTP+SSE transport.
package sse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"sync"
	"time"

	"go.osspkg.com/mcp"
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
	return Config{Address: ":8080", SSEPath: "/sse", MessagePath: "/message", MaxBodyBytes: 1 << 20, SessionTTL: 30 * time.Minute, MaxSessions: 256, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
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
	handler, err := NewHandler(server, transport.Config)
	if err != nil {
		return err
	}
	config := transport.Config
	defaults := DefaultConfig()
	if config.Address == "" {
		config.Address = defaults.Address
	}
	httpServer := Server(ctx, config.Address, handler, config.ReadTimeout, config.WriteTimeout, config.IdleTimeout)
	err = httpServer.ListenAndServe()
	if errors.Is(err, stdhttp.ErrServerClosed) {
		return ctx.Err()
	}
	return err
}

type session struct {
	expiry   time.Time
	messages chan []byte
}

// Handler serves a legacy SSE endpoint and its message endpoint.
type Handler struct {
	server   *mcp.Server
	config   Config
	mu       sync.Mutex
	sessions map[string]*session
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
	return &Handler{server: server, config: config, sessions: map[string]*session{}}, nil
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
	if request.Header.Get("Content-Type") != "application/json" {
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
	response, err := handler.server.ServeJSON(request.Context(), payload, mcp.RequestMeta{Transport: "sse", Headers: headers(request.Header), SessionID: id})
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
		case <-request.Context().Done():
			return
		}
	}
	writer.WriteHeader(stdhttp.StatusAccepted)
}

func (handler *Handler) newSession() (string, *session, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	if len(handler.sessions) >= handler.config.MaxSessions {
		return "", nil, errors.New("session limit")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	id := hex.EncodeToString(raw)
	item := &session{expiry: time.Now().Add(handler.config.SessionTTL), messages: make(chan []byte, 16)}
	handler.sessions[id] = item
	return id, item, nil
}

func (handler *Handler) getSession(id string) *session {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	return handler.sessions[id]
}

func (handler *Handler) deleteSession(id string) {
	handler.mu.Lock()
	delete(handler.sessions, id)
	handler.mu.Unlock()
}

func (handler *Handler) expire() {
	now := time.Now()
	for id, item := range handler.sessions {
		if now.After(item.expiry) {
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

// Server creates an HTTP server that is shut down when ctx is cancelled.
func Server(ctx context.Context, address string, handler stdhttp.Handler, readTimeout, writeTimeout, idleTimeout time.Duration) *stdhttp.Server {
	server := &stdhttp.Server{Addr: address, Handler: handler, ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: idleTimeout}
	go func() { <-ctx.Done(); _ = server.Shutdown(context.Background()) }()
	return server
}
