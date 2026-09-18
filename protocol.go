package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	server.seal()
	var request rpcRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: &rpcError{Code: -32600, Message: "invalid request"}})
	}
	handler := Handler(server.dispatch)
	server.mu.RLock()
	for index := len(server.middleware) - 1; index >= 0; index-- {
		handler = server.middleware[index](handler)
	}
	server.mu.RUnlock()
	result, err := handler(ctx, Request{Method: request.Method, Params: request.Params, Meta: meta})
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
		return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: responseID(request.ID), Error: errorFor(err)})
	}
	return encodeResponse(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
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
	case "prompts/list":
		return server.listPrompts(), nil
	case "prompts/get":
		return server.getPrompt(ctx, request.Params)
	default:
		return nil, protocolError{code: -32601, message: "method not found"}
	}
}

func (server *Server) initialize() map[string]any {
	capabilities := map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}}
	result := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    capabilities,
		"serverInfo":      map[string]string{"name": server.info.Name, "version": server.info.Version},
	}
	if server.info.Instructions != "" {
		result["instructions"] = server.info.Instructions
	}
	return result
}

func (server *Server) listTools() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.tools))
	for _, name := range sortedKeys(server.tools) {
		item := server.tools[name]
		items = append(items, map[string]any{"name": item.name, "description": item.description, "inputSchema": item.inputSchema})
	}
	return map[string]any{"tools": items}
}

func (server *Server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
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
	result, err := item.call(ctx, input.Arguments)
	if err != nil {
		return nil, toolResultError{result: map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "tool execution failed"}},
			"isError": true,
		}}
	}
	return result, nil
}

func (server *Server) listResources() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.resources))
	for _, uri := range sortedKeys(server.resources) {
		item := server.resources[uri]
		items = append(items, map[string]any{"uri": item.URI, "name": item.Name, "description": item.Description, "mimeType": item.MIMEType})
	}
	return map[string]any{"resources": items}
}

func (server *Server) listTemplates() map[string]any {
	server.mu.RLock()
	defer server.mu.RUnlock()
	items := make([]any, 0, len(server.templates))
	for _, item := range server.templates {
		items = append(items, map[string]any{"uriTemplate": item.URITemplate, "name": item.Name, "description": item.Description, "mimeType": item.MIMEType})
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
	templates := append([]ResourceTemplate(nil), server.templates...)
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
	return map[string]any{"contents": []any{map[string]any{"uri": input.URI, "mimeType": item.MIMEType, "text": item.Text}}}, nil
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
			values[part[1:len(part)-1]] = actual[index]
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
		items = append(items, map[string]any{"name": item.Name, "description": item.Description, "arguments": item.Arguments})
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
