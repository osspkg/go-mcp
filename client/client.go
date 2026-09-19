/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package client implements a stdlib-only MCP client for servers using the
// transports in this module. It supports request/response calls, notifications
// and server-initiated sampling, roots, elicitation, and custom requests.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.osspkg.com/mcp"
)

const defaultMaxConcurrentHandlers = 8

var (
	// ErrClosed means that the client or its transport has been closed.
	ErrClosed = errors.New("mcp/client: client is closed")
	// ErrNotStarted means that Start must be called before using the client.
	ErrNotStarted = errors.New("mcp/client: client is not started")
	// ErrStarted means that Start was already called.
	ErrStarted = errors.New("mcp/client: client already started")
	// ErrTransportClosed means that the peer closed the transport.
	ErrTransportClosed = errors.New("mcp/client: transport closed")
)

const (
	rpcVersion       = "2.0"
	resourceURIParam = "uri"
	taskIDParam      = "taskId"
)

func scannerLimit(limit int64) int {
	maxInt := int64(^uint(0) >> 1)
	if limit > maxInt {
		return int(maxInt)
	}
	return int(limit)
}

func responseLimit(limit int64) int64 {
	if limit == int64(^uint64(0)>>1) {
		return limit
	}
	return limit + 1
}

// Receiver is called for every inbound JSON-RPC message from a transport.
type Receiver func(context.Context, []byte) error

// Transport is the wire contract implemented by the stdio, Streamable HTTP,
// and legacy SSE client transports. Start blocks until the transport closes.
type Transport interface {
	Start(ctx context.Context, receiver Receiver) error
	Send(ctx context.Context, payload []byte) ([]byte, error)
	Close() error
}

type readyTransport interface {
	Ready() <-chan struct{}
}

type startupErrorTransport interface {
	StartupError() error
}

// RPCError is a JSON-RPC error returned by an MCP server.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (err *RPCError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message == "" {
		return fmt.Sprintf("mcp/client: rpc error %d", err.Code)
	}
	return fmt.Sprintf("mcp/client: rpc error %d: %s", err.Code, err.Message)
}

// Implementation identifies the client or server during initialization.
type Implementation struct {
	Name        string     `json:"name"`
	Title       string     `json:"title,omitempty"`
	Version     string     `json:"version"`
	Description string     `json:"description,omitempty"`
	WebsiteURL  string     `json:"websiteUrl,omitempty"`
	Icons       []mcp.Icon `json:"icons,omitempty"`
}

// ClientConfig configures a Client's default initialization identity and
// server-initiated handler concurrency.
type ClientConfig struct {
	Info         Implementation
	Capabilities map[string]any
	// MaxConcurrentHandlers bounds simultaneously running server-initiated
	// request and notification handlers. Non-positive values use the default.
	MaxConcurrentHandlers int
}

// InitializeParams are sent in the initialize request.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
}

// InitializeResult is returned by an MCP server during initialization.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

// Tool describes an item returned by tools/list. Schema and annotations are
// preserved as JSON so clients can use extensions without losing information.
type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Icons        []mcp.Icon     `json:"icons,omitempty"`
	Execution    map[string]any `json:"execution,omitempty"`
}

// ToolsListResult is returned by tools/list.
type ToolsListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

//go:generate go run github.com/mailru/easyjson/easyjson -output_filename=client_easyjson.go client.go

// Content is one MCP content block.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	URL      string `json:"url,omitempty"`
}

// CallToolResult is the result of tools/call.
type CallToolResult struct {
	Content           []Content      `json:"content,omitempty"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

// CallToolResponse is either an immediate tool result or a task handle.
type CallToolResponse struct {
	Result *CallToolResult `json:"-"`
	Task   *mcp.Task       `json:"task,omitempty"`
}

// Resource describes an item returned by resources/list.
type Resource struct {
	URI         string     `json:"uri"`
	Name        string     `json:"name"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	MIMEType    string     `json:"mimeType,omitempty"`
	Icons       []mcp.Icon `json:"icons,omitempty"`
}

// ResourceTemplate describes an item returned by resources/templates/list.
type ResourceTemplate struct {
	URITemplate string     `json:"uriTemplate"`
	Name        string     `json:"name"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	MIMEType    string     `json:"mimeType,omitempty"`
	Icons       []mcp.Icon `json:"icons,omitempty"`
}

// ResourcesListResult is returned by resources/list.
type ResourcesListResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// ResourceTemplatesListResult is returned by resources/templates/list.
type ResourceTemplatesListResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
}

// ResourceContents contains text or base64-encoded blob resource data.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ReadResourceResult is returned by resources/read.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

// Prompt describes an item returned by prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Icons       []mcp.Icon       `json:"icons,omitempty"`
}

// PromptArgument describes one prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptsListResult is returned by prompts/list.
type PromptsListResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

// PromptMessage is one message in a prompt result.
type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// GetPromptResult is returned by prompts/get.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// RequestHandler handles a server-initiated JSON-RPC request. Returning an
// error produces a JSON-RPC internal error response to the server.
type RequestHandler func(context.Context, json.RawMessage) (any, error)

// NotificationHandler handles a server notification. Notification errors are
// intentionally not sent back because JSON-RPC notifications have no response.
type NotificationHandler func(context.Context, json.RawMessage) error

type rpcResult struct {
	payload []byte
	err     error
}

type pendingCall struct {
	ch chan rpcResult
}

type inboundMessage struct {
	ctx       context.Context
	id        json.RawMessage
	method    string
	params    json.RawMessage
	isRequest bool
}

// Client is a concurrent-safe MCP JSON-RPC client.
type Client struct {
	transport Transport
	config    ClientConfig

	mu          sync.RWMutex
	started     bool
	closed      bool
	initialized bool
	ctx         context.Context
	cancel      context.CancelFunc
	nextID      atomic.Uint64
	pending     map[string]*pendingCall
	requests    map[string]RequestHandler
	notifies    map[string]NotificationHandler
	inbound     chan inboundMessage
	workers     int
	done        chan struct{}
	doneOnce    sync.Once
}

// New creates a client with the default identity. Call Start before making
// requests.
func New(transport Transport) (*Client, error) {
	return NewWithConfig(transport, ClientConfig{Info: Implementation{Name: "go-mcp-client", Version: "dev"}})
}

// NewWithConfig creates a client with a custom initialization identity.
func NewWithConfig(transport Transport, config ClientConfig) (*Client, error) {
	if transport == nil {
		return nil, errors.New("mcp/client: nil transport")
	}
	if config.Info.Name == "" {
		config.Info.Name = "go-mcp-client"
	}
	if config.Info.Version == "" {
		config.Info.Version = "dev"
	}
	if config.MaxConcurrentHandlers <= 0 {
		config.MaxConcurrentHandlers = defaultMaxConcurrentHandlers
	}
	return &Client{transport: transport, config: config, pending: make(map[string]*pendingCall), requests: make(map[string]RequestHandler), notifies: make(map[string]NotificationHandler), inbound: make(chan inboundMessage, config.MaxConcurrentHandlers), workers: config.MaxConcurrentHandlers, done: make(chan struct{})}, nil
}

// Start starts the transport reader and returns immediately. The transport is
// closed when ctx is cancelled or when Close is called.
func (client *Client) Start(ctx context.Context) error { //nolint:contextcheck // the public API accepts the caller's lifecycle context
	if client == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return ErrClosed
	}
	if client.started {
		client.mu.Unlock()
		return ErrStarted
	}
	client.started = true
	transportCtx, cancel := context.WithCancel(ctx)
	client.ctx = transportCtx
	client.cancel = cancel
	client.mu.Unlock()
	for range client.workers {
		go client.handleInbound(transportCtx)
	}
	go func() {
		err := client.transport.Start(transportCtx, client.receive)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			client.fail(ErrTransportClosed)
			return
		}
		client.fail(err)
	}()
	if ready, ok := client.transport.(readyTransport); ok {
		readyCh := ready.Ready()
		if readyCh == nil {
			return nil
		}
		select {
		case <-readyCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if startup, ok := client.transport.(startupErrorTransport); ok {
		if err := startup.StartupError(); err != nil {
			client.fail(err)
			return err
		}
	}
	return nil
}

// Close closes the transport and rejects outstanding calls.
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return nil
	}
	client.closed = true
	if client.cancel != nil {
		client.cancel()
	}
	for key, call := range client.pending {
		call.ch <- rpcResult{err: ErrClosed}
		delete(client.pending, key)
	}
	client.doneOnce.Do(func() { close(client.done) })
	client.mu.Unlock()
	return client.transport.Close()
}

func (client *Client) fail(err error) {
	if err == nil {
		err = ErrTransportClosed
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return
	}
	client.closed = true
	for key, call := range client.pending {
		call.ch <- rpcResult{err: err}
		delete(client.pending, key)
	}
	client.doneOnce.Do(func() { close(client.done) })
	client.mu.Unlock()
}

func (client *Client) ready() error {
	if client == nil {
		return ErrClosed
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.closed {
		return ErrClosed
	}
	if !client.started {
		return ErrNotStarted
	}
	return nil
}

// OnRequest registers a handler for a server-initiated request such as
// sampling/createMessage, roots/list, or elicitation/create.
func (client *Client) OnRequest(method string, handler RequestHandler) error {
	if method == "" || handler == nil {
		return errors.New("mcp/client: method and handler are required")
	}
	client.mu.Lock()
	client.requests[method] = handler
	client.mu.Unlock()
	return nil
}

// OnNotification registers a handler for server notifications such as
// notifications/progress, notifications/message, and list_changed events.
func (client *Client) OnNotification(method string, handler NotificationHandler) error {
	if method == "" || handler == nil {
		return errors.New("mcp/client: method and handler are required")
	}
	client.mu.Lock()
	client.notifies[method] = handler
	client.mu.Unlock()
	return nil
}

// Call performs a JSON-RPC request and decodes its result into result. A nil
// params value is encoded as an empty object, as required by MCP methods.
func (client *Client) Call(ctx context.Context, method string, params, result any) error {
	payload, err := client.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if result == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, result)
}

// CallRaw performs a JSON-RPC request and returns the raw result JSON.
func (client *Client) CallRaw(ctx context.Context, method string, params any) ([]byte, error) { //nolint:contextcheck // request context is intentionally caller-owned
	if err := client.ready(); err != nil {
		return nil, err
	}
	if method == "" {
		return nil, errors.New("mcp/client: empty method")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := fmt.Sprintf("c-%d", client.nextID.Add(1))
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{rpcVersion, id, method, params})
	if err != nil {
		return nil, err
	}
	call := &pendingCall{ch: make(chan rpcResult, 1)}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return nil, ErrClosed
	}
	client.pending[id] = call
	client.mu.Unlock()
	direct, err := client.transport.Send(ctx, payload)
	if err != nil {
		client.removePending(id)
		return nil, err
	}
	if len(direct) > 0 {
		_ = client.receive(ctx, direct)
	}
	select {
	case result := <-call.ch:
		return result.payload, result.err
	case <-ctx.Done():
		client.removePending(id)
		go client.notifyCancellation(id, ctx.Err()) //nolint:gosec // cancellation notification must outlive the canceled request
		return nil, ctx.Err()
	}
}

func (client *Client) notifyCancellation(id string, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{rpcVersion, "notifications/cancelled", map[string]any{"requestId": id, "reason": cause.Error()}})
	if err == nil {
		_, _ = client.transport.Send(ctx, payload)
	}
}

// Notify sends a JSON-RPC notification.
func (client *Client) Notify(ctx context.Context, method string, params any) error { //nolint:contextcheck // request context is intentionally caller-owned
	if err := client.ready(); err != nil {
		return err
	}
	if method == "" {
		return errors.New("mcp/client: empty method")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{rpcVersion, method, params})
	if err != nil {
		return err
	}
	direct, err := client.transport.Send(ctx, payload)
	if err == nil && len(direct) > 0 {
		_ = client.receive(ctx, direct)
	}
	return err
}

// Initialize performs the MCP initialization handshake and sends the required
// notifications/initialized notification after a successful response.
func (client *Client) Initialize(ctx context.Context, params InitializeParams) (InitializeResult, error) {
	client.mu.RLock()
	defaults := client.config
	client.mu.RUnlock()
	if params.ProtocolVersion == "" {
		params.ProtocolVersion = "2025-11-25"
	}
	if params.Capabilities == nil {
		params.Capabilities = defaults.Capabilities
		if params.Capabilities == nil {
			params.Capabilities = map[string]any{}
		}
	}
	if params.ClientInfo.Name == "" {
		params.ClientInfo = defaults.Info
	}
	var result InitializeResult
	if err := client.Call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, err
	}
	if err := client.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return InitializeResult{}, err
	}
	client.mu.Lock()
	client.initialized = true
	client.mu.Unlock()
	return result, nil
}

// Ping checks that the server is responsive.
func (client *Client) Ping(ctx context.Context) error {
	return client.Call(ctx, "ping", nil, &struct{}{})
}

// ListTools lists the server's tools.
func (client *Client) ListTools(ctx context.Context) (ToolsListResult, error) {
	var result ToolsListResult
	if err := client.Call(ctx, "tools/list", nil, &result); err != nil {
		return ToolsListResult{}, err
	}
	return result, nil
}

// CallTool invokes a tool. If task is non-nil, the server may return a durable
// task handle instead of an immediate result.
func (client *Client) CallTool(ctx context.Context, name string, arguments any, task *mcp.TaskMetadata) (CallToolResponse, error) {
	params := map[string]any{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	if task != nil {
		params["task"] = task
	}
	raw, err := client.CallRaw(ctx, "tools/call", params)
	if err != nil {
		return CallToolResponse{}, err
	}
	var envelope struct {
		Task              *mcp.Task      `json:"task"`
		Content           []Content      `json:"content"`
		StructuredContent map[string]any `json:"structuredContent"`
		IsError           bool           `json:"isError"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return CallToolResponse{}, err
	}
	response := CallToolResponse{Task: envelope.Task}
	if envelope.Task == nil {
		response.Result = &CallToolResult{Content: envelope.Content, StructuredContent: envelope.StructuredContent, IsError: envelope.IsError}
	}
	return response, nil
}

// ListResources lists static resources.
func (client *Client) ListResources(ctx context.Context) (ResourcesListResult, error) {
	var result ResourcesListResult
	if err := client.Call(ctx, "resources/list", nil, &result); err != nil {
		return ResourcesListResult{}, err
	}
	return result, nil
}

// ListResourceTemplates lists URI templates.
func (client *Client) ListResourceTemplates(ctx context.Context) (ResourceTemplatesListResult, error) {
	var result ResourceTemplatesListResult
	if err := client.Call(ctx, "resources/templates/list", nil, &result); err != nil {
		return ResourceTemplatesListResult{}, err
	}
	return result, nil
}

// ReadResource reads a resource by URI.
func (client *Client) ReadResource(ctx context.Context, uri string) (ReadResourceResult, error) {
	var result ReadResourceResult
	if err := client.Call(ctx, "resources/read", map[string]any{resourceURIParam: uri}, &result); err != nil {
		return ReadResourceResult{}, err
	}
	return result, nil
}

// SubscribeResource subscribes to resource update notifications.
func (client *Client) SubscribeResource(ctx context.Context, uri string) error {
	return client.Call(ctx, "resources/subscribe", map[string]any{resourceURIParam: uri}, &struct{}{})
}

// UnsubscribeResource removes a resource subscription.
func (client *Client) UnsubscribeResource(ctx context.Context, uri string) error {
	return client.Call(ctx, "resources/unsubscribe", map[string]any{resourceURIParam: uri}, &struct{}{})
}

// ListPrompts lists prompts exposed by the server.
func (client *Client) ListPrompts(ctx context.Context) (PromptsListResult, error) {
	var result PromptsListResult
	if err := client.Call(ctx, "prompts/list", nil, &result); err != nil {
		return PromptsListResult{}, err
	}
	return result, nil
}

// GetPrompt renders a prompt with arguments.
func (client *Client) GetPrompt(ctx context.Context, name string, arguments map[string]string) (GetPromptResult, error) {
	var result GetPromptResult
	if err := client.Call(ctx, "prompts/get", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return GetPromptResult{}, err
	}
	return result, nil
}

// SetLogLevel changes the server's logging level.
func (client *Client) SetLogLevel(ctx context.Context, level mcp.LogLevel) error {
	return client.Call(ctx, "logging/setLevel", map[string]any{"level": level}, &struct{}{})
}

// GetTask returns durable task state.
func (client *Client) GetTask(ctx context.Context, taskID string) (mcp.Task, error) {
	var result mcp.Task
	if err := client.Call(ctx, "tasks/get", map[string]any{taskIDParam: taskID}, &result); err != nil {
		return mcp.Task{}, err
	}
	return result, nil
}

// GetTaskResultRaw returns the raw result retained by a durable task.
func (client *Client) GetTaskResultRaw(ctx context.Context, taskID string) ([]byte, error) {
	return client.CallRaw(ctx, "tasks/result", map[string]any{taskIDParam: taskID})
}

// GetTaskResult decodes a durable task result into result.
func (client *Client) GetTaskResult(ctx context.Context, taskID string, result any) error {
	raw, err := client.GetTaskResultRaw(ctx, taskID)
	if err != nil || result == nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

// CancelTask requests cancellation of a durable task.
func (client *Client) CancelTask(ctx context.Context, taskID string) (mcp.Task, error) {
	var result mcp.Task
	if err := client.Call(ctx, "tasks/cancel", map[string]any{taskIDParam: taskID}, &result); err != nil {
		return mcp.Task{}, err
	}
	return result, nil
}

// ListTasks returns the server's currently retained tasks.
func (client *Client) ListTasks(ctx context.Context) ([]mcp.Task, error) {
	var result struct {
		Tasks []mcp.Task `json:"tasks"`
	}
	if err := client.Call(ctx, "tasks/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

func (client *Client) removePending(id string) {
	client.mu.Lock()
	delete(client.pending, id)
	client.mu.Unlock()
}

func (client *Client) receive(ctx context.Context, payload []byte) error {
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.JSONRPC != rpcVersion {
		return nil //nolint:nilerr // malformed peer messages are ignored safely
	}
	if envelope.Method != "" {
		client.mu.RLock()
		messageCtx := client.ctx //nolint:contextcheck // use the connection lifecycle context for event callbacks
		client.mu.RUnlock()
		if messageCtx == nil {
			messageCtx = ctx
		}
		if len(envelope.ID) == 0 {
			client.enqueueInbound(inboundMessage{ctx: messageCtx, method: envelope.Method, params: append([]byte(nil), envelope.Params...)})
			return nil
		}
		message := inboundMessage{ctx: messageCtx, id: append([]byte(nil), envelope.ID...), method: envelope.Method, params: append([]byte(nil), envelope.Params...), isRequest: true}
		if !client.enqueueInbound(message) && message.ctx.Err() == nil {
			client.dispatchRequest(message.ctx, message.id, message.method, message.params, errors.New("mcp/client: handler capacity reached"))
		}
		return nil
	}
	key := string(envelope.ID)
	var stringID string
	if json.Unmarshal(envelope.ID, &stringID) == nil {
		key = stringID
	}
	client.mu.Lock()
	call := client.pending[key]
	if call != nil {
		delete(client.pending, key)
	}
	client.mu.Unlock()
	if call != nil {
		if envelope.Error != nil {
			call.ch <- rpcResult{err: envelope.Error}
		} else {
			call.ch <- rpcResult{payload: append([]byte(nil), envelope.Result...)}
		}
	}
	return nil
}

func (client *Client) enqueueInbound(message inboundMessage) bool {
	select {
	case <-message.ctx.Done():
		return false
	case client.inbound <- message:
		return true
	default:
		return false
	}
}

func (client *Client) handleInbound(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-client.inbound:
			if ctx.Err() != nil {
				return
			}
			if message.isRequest {
				client.dispatchRequest(message.ctx, message.id, message.method, message.params, nil) //nolint:contextcheck // preserve the connection lifecycle context from receive
				continue
			}
			client.dispatchNotification(message.ctx, message.method, message.params) //nolint:contextcheck // preserve the connection lifecycle context from receive
		}
	}
}

func (client *Client) dispatchNotification(ctx context.Context, method string, params json.RawMessage) {
	client.mu.RLock()
	handler := client.notifies[method]
	client.mu.RUnlock()
	if handler != nil {
		defer func() { _ = recover() }()
		_ = handler(ctx, params)
	}
}

func (client *Client) dispatchRequest(ctx context.Context, id json.RawMessage, method string, params json.RawMessage, initialErr error) {
	client.mu.RLock()
	handler := client.requests[method]
	client.mu.RUnlock()
	var (
		result any
		err    error
	)
	switch {
	case initialErr != nil:
		err = initialErr
	case handler == nil:
		err = fmt.Errorf("mcp/client: method %s is not supported", method)
	default:
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("mcp/client: request handler panic: %v", recovered)
				}
			}()
			result, err = handler(ctx, params)
		}()
	}
	response := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
		Error   *RPCError       `json:"error,omitempty"`
	}{JSONRPC: rpcVersion, ID: id, Result: result}
	if err != nil {
		response.Result = nil
		response.Error = &RPCError{Code: -32603, Message: err.Error()}
	}
	payload, marshalErr := json.Marshal(response)
	if marshalErr == nil {
		_, _ = client.transport.Send(ctx, payload)
	}
}

// Wait blocks until the client's transport closes or ctx is cancelled.
func (client *Client) Wait(ctx context.Context) error { //nolint:contextcheck // waiting follows the caller's context
	if client == nil {
		return ErrClosed
	}
	if err := client.ready(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client.mu.RLock()
	done := client.done
	client.mu.RUnlock()
	select {
	case <-done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}
