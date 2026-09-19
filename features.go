/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD-3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"context"
	"encoding/json"
	"errors"
)

type peer struct {
	notify NotificationSender
	call   ClientCaller
}

// LogLevel is the severity used by notifications/message.
type LogLevel string

const (
	// LogLevelDebug logs diagnostic details.
	LogLevelDebug LogLevel = "debug"
	// LogLevelInfo logs informational messages.
	LogLevelInfo LogLevel = "info"
	// LogLevelNotice logs noteworthy but non-warning messages.
	LogLevelNotice LogLevel = "notice"
	// LogLevelWarning logs recoverable problems.
	LogLevelWarning LogLevel = "warning"
	// LogLevelError logs errors.
	LogLevelError LogLevel = "error"
	// LogLevelCritical logs critical failures.
	LogLevelCritical LogLevel = "critical"
	// LogLevelAlert logs alerts requiring attention.
	LogLevelAlert LogLevel = "alert"
	// LogLevelEmergency logs emergency failures.
	LogLevelEmergency LogLevel = "emergency"
)

func validLogLevel(level LogLevel) bool {
	switch level {
	case LogLevelDebug, LogLevelInfo, LogLevelNotice, LogLevelWarning, LogLevelError, LogLevelCritical, LogLevelAlert, LogLevelEmergency:
		return true
	default:
		return false
	}
}

// SamplingMessage is a message sent to a client's sampling model.
type SamplingMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ModelPreferences describes a server's preferred sampling model characteristics.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         *float64    `json:"costPriority,omitempty"`
	SpeedPriority        *float64    `json:"speedPriority,omitempty"`
	IntelligencePriority *float64    `json:"intelligencePriority,omitempty"`
}

// ModelHint is a partial model name hint used by sampling.
type ModelHint struct {
	Name string `json:"name,omitempty"`
}

// SamplingTool describes a tool made available to a sampling model.
type SamplingTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolChoice controls sampling tool selection.
type ToolChoice struct {
	Mode string `json:"mode"`
}

// CreateMessageParams are parameters for sampling/createMessage.
type CreateMessageParams struct {
	Messages         []SamplingMessage `json:"messages"`
	MaxTokens        int               `json:"maxTokens"`
	SystemPrompt     string            `json:"systemPrompt,omitempty"`
	IncludeContext   string            `json:"includeContext,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	StopSequences    []string          `json:"stopSequences,omitempty"`
	ModelPreferences *ModelPreferences `json:"modelPreferences,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
	Tools            []SamplingTool    `json:"tools,omitempty"`
	ToolChoice       *ToolChoice       `json:"toolChoice,omitempty"`
	Task             *TaskMetadata     `json:"task,omitempty"`
}

// CreateMessageResult is the client's response to sampling/createMessage.
type CreateMessageResult struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stopReason,omitempty"`
}

// Sample asks the connected client to perform model sampling.
func (request Request) Sample(ctx context.Context, params CreateMessageParams) (CreateMessageResult, error) {
	var result CreateMessageResult
	response, err := request.CallClient(ctx, "sampling/createMessage", params)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, err
	}
	return result, nil
}

// Root identifies a URI boundary offered by a client.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// ListRootsResult is the response to roots/list.
type ListRootsResult struct {
	Roots []Root `json:"roots"`
}

// ListRoots asks the connected client for its roots.
func (request Request) ListRoots(ctx context.Context) (ListRootsResult, error) {
	var result ListRootsResult
	response, err := request.CallClient(ctx, "roots/list", map[string]any{})
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, err
	}
	return result, nil
}

// ElicitRequest describes either form-mode or URL-mode elicitation.
type ElicitRequest struct {
	ElicitationID   string         `json:"elicitationId,omitempty"`
	Mode            string         `json:"mode,omitempty"`
	Message         string         `json:"message"`
	RequestedSchema map[string]any `json:"requestedSchema,omitempty"`
	URL             string         `json:"url,omitempty"`
}

const elicitationModeForm = "form"

// ElicitResult is the client's response to elicitation/create.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
	Meta    map[string]any `json:"_meta,omitempty"`
}

// URLElicitationRequiredError asks a client to complete one or more URL-mode
// elicitations before the original operation can continue.
type URLElicitationRequiredError struct {
	Elicitations []ElicitRequest
}

func (err URLElicitationRequiredError) Error() string { return "mcp: URL elicitation required" }

// Elicit asks a connected client for user input.
func (request Request) Elicit(ctx context.Context, params ElicitRequest) (ElicitResult, error) {
	var result ElicitResult
	if params.Message == "" {
		return result, errors.New("mcp: elicitation message is required")
	}
	if params.Mode == "" {
		params.Mode = elicitationModeForm
	}
	if params.Mode != "form" && params.Mode != "url" {
		return result, errors.New("mcp: unsupported elicitation mode")
	}
	if params.Mode == "url" && (params.URL == "" || params.ElicitationID == "") {
		return result, errors.New("mcp: URL elicitation requires elicitation ID and URL")
	}
	if params.Mode == elicitationModeForm && params.RequestedSchema == nil {
		return result, errors.New("mcp: form elicitation requires requested schema")
	}
	response, err := request.CallClient(ctx, "elicitation/create", params)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return result, err
	}
	return result, nil
}

// NotifyToolsListChanged sends the standard catalog-change notification to a
// connected client.
func (server *Server) NotifyToolsListChanged(ctx context.Context, sessionID string) error {
	return server.NotifySession(ctx, sessionID, "notifications/tools/list_changed", map[string]any{})
}

// NotifyResourcesListChanged sends the standard catalog-change notification.
func (server *Server) NotifyResourcesListChanged(ctx context.Context, sessionID string) error {
	return server.NotifySession(ctx, sessionID, "notifications/resources/list_changed", map[string]any{})
}

// NotifyPromptsListChanged sends the standard catalog-change notification.
func (server *Server) NotifyPromptsListChanged(ctx context.Context, sessionID string) error {
	return server.NotifySession(ctx, sessionID, "notifications/prompts/list_changed", map[string]any{})
}

// NotifyTaskStatus sends an optional durable-task status notification.
func (server *Server) NotifyTaskStatus(ctx context.Context, sessionID string, task Task) error {
	return server.NotifySession(ctx, sessionID, "notifications/tasks/status", task)
}

// NotifyResourceUpdated sends a resource subscription update notification.
func (server *Server) NotifyResourceUpdated(ctx context.Context, sessionID, uri string) error {
	if uri == "" {
		return errors.New("mcp: resource URI is required")
	}
	if sessionID != "" {
		return server.NotifySession(ctx, sessionID, "notifications/resources/updated", map[string]any{"uri": uri}) //nolint:goconst // protocol field name
	}
	server.mu.RLock()
	sessions := make([]string, 0, len(server.subscriptions))
	for id, items := range server.subscriptions {
		if _, subscribed := items[uri]; subscribed {
			sessions = append(sessions, id)
		}
	}
	server.mu.RUnlock()
	var firstErr error
	for _, id := range sessions {
		if err := server.NotifySession(ctx, id, "notifications/resources/updated", map[string]any{"uri": uri}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (server *Server) subscribe(sessionID, uri string) {
	if sessionID == "" || uri == "" {
		return
	}
	server.mu.Lock()
	if server.subscriptions[sessionID] == nil {
		server.subscriptions[sessionID] = map[string]struct{}{}
	}
	server.subscriptions[sessionID][uri] = struct{}{}
	server.mu.Unlock()
}

func (server *Server) unsubscribe(sessionID, uri string) {
	server.mu.Lock()
	if items := server.subscriptions[sessionID]; items != nil {
		delete(items, uri)
		if len(items) == 0 {
			delete(server.subscriptions, sessionID)
		}
	}
	server.mu.Unlock()
}

func (server *Server) registerPeer(sessionID string, meta RequestMeta) {
	if sessionID == "" || (meta.Notify == nil && meta.Call == nil) {
		return
	}
	server.mu.Lock()
	server.peers[sessionID] = peer{notify: meta.Notify, call: meta.Call}
	server.mu.Unlock()
}

// UnregisterPeer removes callbacks associated with a disconnected session.
func (server *Server) UnregisterPeer(sessionID string) {
	if sessionID == "" {
		return
	}
	server.mu.Lock()
	delete(server.peers, sessionID)
	delete(server.subscriptions, sessionID)
	server.mu.Unlock()
}

// NotifySession sends a server notification on a registered connection.
func (server *Server) NotifySession(ctx context.Context, sessionID, method string, params any) error {
	server.mu.RLock()
	item, ok := server.peers[sessionID]
	server.mu.RUnlock()
	if !ok || item.notify == nil {
		return errors.New("mcp: session notifications are unavailable")
	}
	return item.notify(ctx, method, params)
}

// CallSession sends a server request on a registered connection and waits for
// its JSON-RPC result.
func (server *Server) CallSession(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	server.mu.RLock()
	item, ok := server.peers[sessionID]
	server.mu.RUnlock()
	if !ok || item.call == nil {
		return nil, errors.New("mcp: session client requests are unavailable")
	}
	return item.call(ctx, method, params)
}
