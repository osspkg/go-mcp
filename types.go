/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package mcp implements a small, stdlib-only server for the Model Context
// Protocol revision 2025-11-25.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnauthorized tells a transport that the caller must authenticate.
	ErrUnauthorized = errors.New("mcp: unauthorized")
	// ErrForbidden tells a transport that the caller is authenticated but may not act.
	ErrForbidden = errors.New("mcp: forbidden")
	// ErrStarted is returned when a registry is changed after it is sealed.
	ErrStarted = errors.New("mcp: server already started")
)

// ServerInfo identifies a server during MCP initialization.
type ServerInfo struct {
	Name         string
	Version      string
	Description  string
	Instructions string
	Icons        []Icon
}

// Icon describes an icon advertised by an MCP implementation or capability.
type Icon struct {
	Src      string `json:"src"`
	MIMEType string `json:"mimeType,omitempty"`
	Theme    string `json:"theme,omitempty"`
	Size     string `json:"size,omitempty"`
}

// RequestMeta is transport-provided request information available to middleware.
type RequestMeta struct {
	Transport string
	Headers   map[string]string
	SessionID string
	Notify    NotificationSender
	Call      ClientCaller
}

// NotificationSender sends a JSON-RPC notification on the active connection.
type NotificationSender func(context.Context, string, any) error

// ClientCaller sends a server-initiated JSON-RPC request to the connected client.
type ClientCaller func(context.Context, string, any) (json.RawMessage, error)

// Request is the parsed JSON-RPC request passed through middleware.
type Request struct {
	Method string
	Params json.RawMessage
	Meta   RequestMeta
}

// SendNotification sends a notification when the transport supports it.
func (request Request) SendNotification(ctx context.Context, method string, params any) error {
	if request.Meta.Notify == nil {
		return errors.New("mcp: client notifications are unavailable")
	}
	return request.Meta.Notify(ctx, method, params)
}

// CallClient sends a request to the connected MCP client.
func (request Request) CallClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if request.Meta.Call == nil {
		return nil, errors.New("mcp: client requests are unavailable")
	}
	return request.Meta.Call(ctx, method, params)
}

// Handler processes one MCP request.
type Handler func(context.Context, Request) (any, error)

// Middleware wraps every MCP request, including initialize and list methods.
type Middleware func(Handler) Handler

// RequestObserver receives completed, successfully parsed MCP requests.
// Observers run synchronously and should return quickly.
type RequestObserver func(context.Context, Request, time.Duration)

// Option configures a Server.
type Option func(*Server) error

// WithCapabilities sets additional server capability metadata advertised during initialize.
func WithCapabilities(capabilities map[string]any) Option {
	return func(server *Server) error {
		server.capabilities = mapsClone(capabilities)
		return nil
	}
}

// WithMiddleware appends middleware to the request chain. Middleware run in
// the order in which options are supplied.
func WithMiddleware(middleware ...Middleware) Option {
	return func(server *Server) error {
		for _, item := range middleware {
			if item == nil {
				return errors.New("mcp: nil middleware")
			}
			server.middleware = append(server.middleware, item)
		}
		return nil
	}
}

// WithRequestObserver appends observers invoked after each parsed request.
func WithRequestObserver(observers ...RequestObserver) Option {
	return func(server *Server) error {
		for _, observer := range observers {
			if observer == nil {
				return errors.New("mcp: nil request observer")
			}
			server.observers = append(server.observers, observer)
		}
		return nil
	}
}

// Server is a transport-independent MCP server. Register all catalog entries
// before serving; the first request seals the registry.
type Server struct {
	info ServerInfo

	mu           sync.RWMutex
	started      bool
	middleware   []Middleware
	observers    []RequestObserver
	tools        map[string]tool
	resources    map[string]Resource
	templates    []ResourceTemplate
	prompts      map[string]Prompt
	active       map[string]context.CancelFunc
	capabilities map[string]any
}

func mapsClone(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// New creates a Server identified by info.
func New(info ServerInfo, options ...Option) (*Server, error) {
	if strings.TrimSpace(info.Name) == "" {
		return nil, errors.New("mcp: server name is required")
	}
	if strings.TrimSpace(info.Version) == "" {
		return nil, errors.New("mcp: server version is required")
	}
	server := &Server{
		info:      info,
		tools:     map[string]tool{},
		resources: map[string]Resource{},
		prompts:   map[string]Prompt{},
		active:    map[string]context.CancelFunc{},
	}
	server.info.Icons = slices.Clone(info.Icons)
	for _, option := range options {
		if option == nil {
			return nil, errors.New("mcp: nil option")
		}
		if err := option(server); err != nil {
			return nil, err
		}
	}
	return server, nil
}

func (server *Server) seal() {
	server.mu.Lock()
	server.started = true
	server.mu.Unlock()
}

func (server *Server) registerTool(item tool) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		return ErrStarted
	}
	if _, exists := server.tools[item.name]; exists {
		return fmt.Errorf("mcp: duplicate tool %q", item.name)
	}
	server.tools[item.name] = item
	return nil
}

type tool struct {
	name        string
	description string
	inputSchema map[string]any
	call        func(context.Context, json.RawMessage) (any, error)
}

// ToolHandler processes typed input and produces typed output.
type ToolHandler[In json.Unmarshaler, Out json.Marshaler] func(context.Context, In) (Out, error)

// RegisterTool adds a typed MCP tool. In and Out must be pointers to structs
// implementing json.Unmarshaler and json.Marshaler respectively.
func RegisterTool[In json.Unmarshaler, Out json.Marshaler](server *Server, name, description string, handler ToolHandler[In, Out]) error {
	if server == nil {
		return errors.New("mcp: nil server")
	}
	if strings.TrimSpace(name) == "" || handler == nil {
		return errors.New("mcp: tool name and handler are required")
	}
	typeOfInput := reflect.TypeFor[In]()
	schema, err := schemaFor(typeOfInput)
	if err != nil {
		return fmt.Errorf("mcp: tool %q schema: %w", name, err)
	}
	return server.registerTool(tool{
		name: name, description: description, inputSchema: schema,
		call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			value := reflect.New(typeOfInput.Elem()).Interface()
			input, ok := value.(In)
			if !ok {
				return nil, fmt.Errorf("mcp: tool %q input must be a pointer to struct", name)
			}
			if err := json.Unmarshal(raw, input); err != nil {
				return nil, fmt.Errorf("invalid tool arguments: %w", err)
			}
			output, err := handler(ctx, input)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return nil, fmt.Errorf("encoding tool result: %w", err)
			}
			var structured any
			if err := json.Unmarshal(encoded, &structured); err != nil {
				return nil, fmt.Errorf("decoding tool result: %w", err)
			}
			return map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
				"structuredContent": structured,
			}, nil
		},
	})
}

func schemaFor(value reflect.Type) (map[string]any, error) {
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		return nil, errors.New("model must be a pointer to struct")
	}
	return schemaForType(value, map[reflect.Type]bool{})
}

func schemaForStruct(value reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	properties := map[string]any{}
	required := []string{}
	for index := range value.NumField() {
		field := value.Field(index)
		if !field.IsExported() {
			continue
		}
		jsonTag := strings.Split(field.Tag.Get("json"), ",")
		name := jsonTag[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		property, err := schemaForType(field.Type, visiting)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if description := tagDescription(field.Tag.Get("mcp")); description != "" {
			property["description"] = description
		}
		properties[name] = property
		optional := false
		for _, option := range jsonTag[1:] {
			optional = optional || option == "omitempty"
		}
		if !optional {
			required = append(required, name)
		}
	}
	schema := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}

func schemaForType(value reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		if visiting[value] {
			return nil, fmt.Errorf("recursive Go type %s", value)
		}
		visiting[value] = true
		defer delete(visiting, value)
		items, err := schemaForType(value.Elem(), visiting)
		return map[string]any{"type": "array", "items": items}, err
	case reflect.Struct:
		if visiting[value] {
			return nil, fmt.Errorf("recursive Go type %s", value)
		}
		visiting[value] = true
		defer delete(visiting, value)
		return schemaForStruct(value, visiting)
	default:
		return nil, fmt.Errorf("unsupported Go type %s", value)
	}
}

func tagDescription(tag string) string {
	for _, item := range strings.Split(tag, ",") {
		if description, ok := strings.CutPrefix(item, "description="); ok {
			return description
		}
	}
	return ""
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
