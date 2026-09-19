/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	mimeTypeField = "mimeType"
	uriField      = "uri"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolResultError struct{ result any }

func (err toolResultError) Error() string { return "tool execution returned an error result" }

type protocolError struct {
	code    int
	message string
}

func (err protocolError) Error() string { return err.message }

// ServeJSON processes one JSON-RPC request. A nil response represents a JSON-RPC notification.
func (server *Server) ServeJSON(ctx context.Context, payload []byte, meta RequestMeta) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background() //nolint:contextcheck // a nil transport context has no parent
	}
	server.seal()
	var request rpcRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}) //nolint:goconst // JSON-RPC wire literal
	}
	if request.JSONRPC != "2.0" || request.Method == "" || !validRequestID(request.ID) || !validParams(request.Params) {
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: &rpcError{Code: -32600, Message: "invalid request"}})
	}
	handler := Handler(server.dispatch)
	server.mu.RLock()
	for index := len(server.middleware) - 1; index >= 0; index-- {
		handler = server.middleware[index](handler)
	}
	server.mu.RUnlock()
	observed := Request{Method: request.Method, Params: request.Params, Meta: meta}
	server.registerPeer(meta.SessionID, meta)
	started := time.Now()
	defer func() { server.observe(ctx, observed, time.Since(started)) }()
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	requestCtx = context.WithValue(requestCtx, requestContextKey{}, observed)
	if len(request.ID) > 0 {
		server.trackRequest(request.ID, cancel)
		defer server.untrackRequest(request.ID)
	}
	result, err := invokeHandler(requestCtx, handler, observed)
	if len(request.ID) == 0 {
		return nil, nil
	}
	if err != nil {
		var protocolErr protocolError
		if errors.As(err, &protocolErr) {
			return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: &rpcError{Code: protocolErr.code, Message: protocolErr.message}})
		}
		var toolErr toolResultError
		if errors.As(err, &toolErr) {
			return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Result: toolErr.result})
		}
		var elicitationErr URLElicitationRequiredError
		if errors.As(err, &elicitationErr) {
			return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: &rpcError{Code: -32042, Message: "URL elicitation required", Data: map[string]any{"elicitations": elicitationErr.Elicitations}}})
		}
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: errorFor(err)})
	}
	return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func invokeHandler(ctx context.Context, handler Handler, request Request) (result any, err error) { //nolint:nonamedreturns // panic recovery replaces the returned error
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("mcp: handler panic: %v", recovered)
		}
	}()
	return handler(ctx, request)
}

func (server *Server) observe(ctx context.Context, request Request, duration time.Duration) {
	server.mu.RLock()
	observers := append([]RequestObserver(nil), server.observers...)
	server.mu.RUnlock()
	for _, observer := range observers {
		observer(ctx, request, duration)
	}
}

func (server *Server) trackRequest(id json.RawMessage, cancel context.CancelFunc) {
	server.mu.Lock()
	server.active[string(id)] = cancel
	server.mu.Unlock()
}

func (server *Server) untrackRequest(id json.RawMessage) {
	server.mu.Lock()
	delete(server.active, string(id))
	server.mu.Unlock()
}

func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, float64:
		return true
	default:
		return false
	}
}

func validParams(params json.RawMessage) bool {
	if len(params) == 0 {
		return true
	}
	var value any
	if json.Unmarshal(params, &value) != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func encodeResponse(response rpcResponse) ([]byte, error) {
	return json.Marshal(response)
}

func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func errorFor(err error) *rpcError {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return &rpcError{Code: -32001, Message: "unauthorized"}
	case errors.Is(err, ErrForbidden):
		return &rpcError{Code: -32003, Message: "forbidden"}
	default:
		return &rpcError{Code: -32603, Message: "internal error"}
	}
}

func (server *Server) dispatch(ctx context.Context, request Request) (any, error) {
	switch request.Method {
	case "initialize":
		return server.initialize(), nil
	case "ping":
		return map[string]any{}, nil
	case "$/cancelRequest":
		return nil, server.cancelRequest(request.Params)
	case "notifications/cancelled":
		return nil, server.cancelRequest(request.Params)
	case "notifications/elicitation/complete":
		return map[string]any{}, nil
	case "logging/setLevel":
		return server.setLogLevel(request.Params)
	case "tasks/get":
		return server.getTask(ctx, request.Params)
	case "tasks/result":
		return server.getTaskResult(request.Params)
	case "tasks/cancel":
		return server.cancelTask(ctx, request.Params)
	case "tasks/list":
		return server.listTasks()
	case "tools/list":
		return server.listTools(), nil
	case "tools/call":
		return server.callTool(ctx, request.Params)
	case "resources/list":
		return server.listResources(), nil
	case "resources/templates/list":
		return server.listTemplates(), nil
	case "resources/read":
		return server.readResource(ctx, request.Params)
	case "resources/subscribe":
		return server.subscribeResource(request)
	case "resources/unsubscribe":
		return server.unsubscribeResource(request)
	case "prompts/list":
		return server.listPrompts(), nil
	case "prompts/get":
		return server.getPrompt(ctx, request.Params)
	default:
		return nil, protocolError{code: -32601, message: "method not found"}
	}
}

func (server *Server) setLogLevel(params json.RawMessage) (any, error) {
	var input struct {
		Level LogLevel `json:"level"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !validLogLevel(input.Level) {
		return nil, protocolError{code: -32602, message: "invalid logging level"}
	}
	server.mu.Lock()
	server.logLevel = input.Level
	server.mu.Unlock()
	return map[string]any{}, nil
}

func (server *Server) subscribeResource(request Request) (any, error) {
	var input struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(request.Params, &input); err != nil || input.URI == "" {
		return nil, protocolError{code: -32602, message: "invalid resource subscription"}
	}
	server.subscribe(request.Meta.SessionID, input.URI)
	return map[string]any{}, nil
}

func (server *Server) unsubscribeResource(request Request) (any, error) {
	var input struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(request.Params, &input); err != nil || input.URI == "" {
		return nil, protocolError{code: -32602, message: "invalid resource subscription"}
	}
	server.unsubscribe(request.Meta.SessionID, input.URI)
	return map[string]any{}, nil
}

func (server *Server) cancelRequest(params json.RawMessage) error {
	var input struct {
		ID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !validRequestID(input.ID) || len(input.ID) == 0 {
		return protocolError{code: -32602, message: "invalid cancellation parameters"}
	}
	server.mu.RLock()
	cancel := server.active[string(input.ID)]
	server.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (server *Server) initialize() map[string]any {
	capabilities := mapsClone(server.capabilities)
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	server.mu.RLock()
	hasTools := len(server.tools) > 0
	hasResources := len(server.resources) > 0 || len(server.templates) > 0
	hasPrompts := len(server.prompts) > 0
	server.mu.RUnlock()
	if hasTools {
		if _, exists := capabilities["tools"]; !exists {
			capabilities["tools"] = map[string]any{"listChanged": true} //nolint:goconst // capability field name
		}
	}
	if hasResources {
		if _, exists := capabilities["resources"]; !exists {
			capabilities["resources"] = map[string]any{"listChanged": true, "subscribe": true}
		}
	}
	if hasPrompts {
		if _, exists := capabilities["prompts"]; !exists {
			capabilities["prompts"] = map[string]any{"listChanged": true}
		}
	}
	if _, exists := capabilities["logging"]; !exists {
		capabilities["logging"] = map[string]any{}
	}
	if _, exists := capabilities["tasks"]; !exists {
		capabilities["tasks"] = map[string]any{"cancel": map[string]any{}, "list": map[string]any{}, "requests": map[string]any{"tools": map[string]any{"call": map[string]any{}}}}
	}
	serverInfo := map[string]any{"name": server.info.Name, "version": server.info.Version} //nolint:goconst // protocol field names
	result := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    capabilities,
		"serverInfo":      serverInfo,
	}
	if server.info.Instructions != "" {
		result["instructions"] = server.info.Instructions
	}
	if server.info.Description != "" {
		serverInfo["description"] = server.info.Description
	}
	if server.info.Title != "" {
		serverInfo["title"] = server.info.Title
	}
	if server.info.WebsiteURL != "" {
		serverInfo["websiteUrl"] = server.info.WebsiteURL
	}
	if len(server.info.Icons) > 0 {
		serverInfo["icons"] = server.info.Icons
	}
	return result
}

func (server *Server) listTools() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.tools))
	for _, name := range sortedKeys(server.tools) {
		item := server.tools[name]
		entry := map[string]any{"name": item.name, "description": item.description, "inputSchema": item.inputSchema} //nolint:goconst // protocol field names
		if item.outputSchema != nil {
			entry["outputSchema"] = item.outputSchema
		}
		if item.annotations != nil {
			entry["annotations"] = item.annotations
		}
		if len(item.icons) > 0 {
			entry["icons"] = item.icons
		}
		if item.taskSupport != "" {
			entry["execution"] = map[string]any{"taskSupport": item.taskSupport}
		}
		items = append(items, entry)
	}
	return map[string]any{"tools": items}
}

func toolErrorResult() map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "tool execution failed"}}, //nolint:goconst // protocol field name
		"isError": true,
	}
}

func (server *Server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Task      *TaskMetadata   `json:"task,omitempty"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Name == "" {
		return nil, protocolError{code: -32602, message: "invalid tool parameters"}
	}
	server.mu.RLock()
	item, ok := server.tools[input.Name]
	server.mu.RUnlock()
	if !ok {
		return nil, protocolError{code: -32602, message: "unknown tool"}
	}
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage("{}")
	}
	if input.Task != nil {
		if input.Task.TTL < 0 || input.Task.TTL > maxTaskTTLMillis {
			return nil, protocolError{code: -32602, message: "invalid task TTL"}
		}
		ttl := time.Duration(input.Task.TTL) * time.Millisecond
		task, err := server.startTask(ctx, ttl, func(taskCtx context.Context) (any, error) {
			result, callErr := item.call(taskCtx, input.Arguments)
			if callErr != nil {
				return toolErrorResult(), nil //nolint:nilerr // tool errors are protocol results
			}
			return result, nil
		}, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": task}, nil
	}
	result, err := item.call(ctx, input.Arguments)
	if err != nil {
		return nil, toolResultError{result: toolErrorResult()}
	}
	return result, nil
}

func (server *Server) getTask(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.TaskID == "" {
		return nil, protocolError{code: -32602, message: "invalid task parameters"} //nolint:goconst // shared protocol error
	}
	task, err := server.GetTask(ctx, input.TaskID)
	if err != nil {
		return nil, protocolError{code: -32004, message: "task not found"} //nolint:goconst // shared protocol error
	}
	return task, nil
}

func (server *Server) getTaskResult(params json.RawMessage) (any, error) {
	var input struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.TaskID == "" {
		return nil, protocolError{code: -32602, message: "invalid task parameters"}
	}
	result, err := server.taskResult(input.TaskID)
	if err != nil {
		if errors.Is(err, ErrTaskNotReady) {
			return nil, protocolError{code: -32005, message: "task result is not ready"}
		}
		if errors.Is(err, ErrTaskNotFound) {
			return nil, protocolError{code: -32004, message: "task not found"}
		}
		return nil, err
	}
	return result, nil
}

func (server *Server) cancelTask(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.TaskID == "" {
		return nil, protocolError{code: -32602, message: "invalid task parameters"}
	}
	task, err := server.CancelTask(ctx, input.TaskID)
	if err != nil {
		return nil, protocolError{code: -32004, message: "task not found"}
	}
	return task, nil
}

func (server *Server) listTasks() (any, error) {
	server.mu.Lock()
	server.cleanupTasksLocked(time.Now())
	items := make([]Task, 0, len(server.tasks))
	for _, record := range server.tasks {
		items = append(items, record.snapshot())
	}
	server.mu.Unlock()
	sort.Slice(items, func(left, right int) bool { return items[left].TaskID < items[right].TaskID })
	return map[string]any{"tasks": items}, nil
}

func (server *Server) listResources() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.resources))
	for _, uri := range sortedKeys(server.resources) {
		item := server.resources[uri]
		entry := map[string]any{uriField: item.URI, "name": item.Name, "description": item.Description, mimeTypeField: item.MIMEType}
		if len(item.Icons) > 0 {
			entry["icons"] = item.Icons
		}
		items = append(items, entry)
	}
	return map[string]any{"resources": items}
}

func (server *Server) listTemplates() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.templates))
	for _, item := range server.templates {
		entry := map[string]any{"uriTemplate": item.URITemplate, "name": item.Name, "description": item.Description, mimeTypeField: item.MIMEType}
		if len(item.Icons) > 0 {
			entry["icons"] = item.Icons
		}
		items = append(items, entry)
	}
	return map[string]any{"resourceTemplates": items}
}

func (server *Server) readResource(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URI == "" {
		return nil, protocolError{code: -32602, message: "invalid resource parameters"}
	}
	server.mu.RLock()
	item, static := server.resources[input.URI]
	var templates []ResourceTemplate
	if !static {
		templates = append([]ResourceTemplate(nil), server.templates...)
	}
	server.mu.RUnlock()
	if !static {
		for _, template := range templates {
			if variables, ok := matchTemplate(template.URITemplate, input.URI); ok {
				var err error
				item, err = template.Handler(ctx, ResourceRequest{URI: input.URI, Variables: variables})
				if err != nil {
					return nil, err
				}
				static = true
				break
			}
		}
	}
	if !static {
		return nil, protocolError{code: -32004, message: "resource not found"}
	}
	return map[string]any{"contents": []any{map[string]any{uriField: input.URI, mimeTypeField: item.MIMEType, "text": item.Text}}}, nil
}

func matchTemplate(template, uri string) (map[string]string, bool) {
	parts := strings.Split(template, "/")
	actual := strings.Split(uri, "/")
	if len(parts) != len(actual) {
		return nil, false
	}
	values := map[string]string{}
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			value, err := url.PathUnescape(actual[index])
			if err != nil || value == "" || strings.Contains(value, "/") {
				return nil, false
			}
			values[part[1:len(part)-1]] = value
			continue
		}
		if part != actual[index] {
			return nil, false
		}
	}
	return values, true
}

func (server *Server) listPrompts() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.prompts))
	for _, name := range sortedKeys(server.prompts) {
		item := server.prompts[name]
		entry := map[string]any{"name": item.Name, "description": item.Description, "arguments": item.Arguments}
		if len(item.Icons) > 0 {
			entry["icons"] = item.Icons
		}
		items = append(items, entry)
	}
	return map[string]any{"prompts": items}
}

func (server *Server) getPrompt(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Name == "" {
		return nil, protocolError{code: -32602, message: "invalid prompt parameters"}
	}
	server.mu.RLock()
	prompt, ok := server.prompts[input.Name]
	server.mu.RUnlock()
	if !ok {
		return nil, protocolError{code: -32004, message: "prompt not found"}
	}
	messages := prompt.Messages
	if prompt.Handler != nil {
		var err error
		messages, err = prompt.Handler(ctx, input.Arguments)
		if err != nil {
			return nil, err
		}
	}
	result := make([]any, 0, len(messages))
	for _, message := range messages {
		result = append(result, map[string]any{"role": message.Role, "content": map[string]string{"type": "text", "text": message.Text}})
	}
	return map[string]any{"messages": result}, nil
}
