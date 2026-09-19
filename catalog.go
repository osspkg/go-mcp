/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Resource is a static MCP resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Text        string `json:"text,omitempty"`
	Icons       []Icon `json:"icons,omitempty"`
}

// ResourceRequest identifies a dynamic resource request.
type ResourceRequest struct {
	URI       string
	Variables map[string]string
}

// ResourceHandler reads a dynamic resource.
type ResourceHandler func(context.Context, ResourceRequest) (Resource, error)

// ResourceTemplate maps a URI template such as file:///{name} to a resource.
type ResourceTemplate struct {
	URITemplate string          `json:"uriTemplate"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	MIMEType    string          `json:"mimeType,omitempty"`
	Handler     ResourceHandler `json:"-"`
	Icons       []Icon          `json:"icons,omitempty"`
}

// RegisterResource registers a static resource.
func (server *Server) RegisterResource(resource Resource) error {
	if strings.TrimSpace(resource.URI) == "" || strings.TrimSpace(resource.Name) == "" {
		return errors.New("mcp: resource URI and name are required")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		return ErrStarted
	}
	if _, exists := server.resources[resource.URI]; exists {
		return fmt.Errorf("mcp: duplicate resource %q", resource.URI)
	}
	resource.Icons = cloneIcons(resource.Icons)
	server.resources[resource.URI] = resource
	return nil
}

// RegisterResourceTemplate registers a dynamic URI template.
func (server *Server) RegisterResourceTemplate(template ResourceTemplate) error {
	if strings.TrimSpace(template.URITemplate) == "" || strings.TrimSpace(template.Name) == "" || template.Handler == nil {
		return errors.New("mcp: resource template, name, and handler are required")
	}
	if !validTemplate(template.URITemplate) {
		return errors.New("mcp: invalid resource template")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		return ErrStarted
	}
	for _, item := range server.templates {
		if item.URITemplate == template.URITemplate {
			return fmt.Errorf("mcp: duplicate resource template %q", template.URITemplate)
		}
	}
	template.Icons = cloneIcons(template.Icons)
	server.templates = append(server.templates, template)
	return nil
}

func validTemplate(template string) bool {
	for _, part := range strings.Split(template, "/") {
		if !strings.ContainsAny(part, "{}") {
			continue
		}
		if len(part) < 3 || part[0] != '{' || part[len(part)-1] != '}' {
			return false
		}
		for index, char := range part[1 : len(part)-1] {
			if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (index <= 0 || char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

// PromptMessage is one message returned by a prompt.
type PromptMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// PromptArgument describes a prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt is an MCP prompt. Handler is optional for static prompts.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Messages    []PromptMessage  `json:"-"`
	Handler     PromptHandler    `json:"-"`
	Icons       []Icon           `json:"icons,omitempty"`
}

// PromptHandler produces messages from string arguments.
type PromptHandler func(context.Context, map[string]string) ([]PromptMessage, error)

// RegisterPrompt registers a static or dynamic prompt.
func (server *Server) RegisterPrompt(prompt Prompt) error {
	if strings.TrimSpace(prompt.Name) == "" {
		return errors.New("mcp: prompt name is required")
	}
	if prompt.Handler == nil && len(prompt.Messages) == 0 {
		return errors.New("mcp: prompt needs messages or handler")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.started {
		return ErrStarted
	}
	if _, exists := server.prompts[prompt.Name]; exists {
		return fmt.Errorf("mcp: duplicate prompt %q", prompt.Name)
	}
	prompt.Arguments = append([]PromptArgument(nil), prompt.Arguments...)
	prompt.Messages = append([]PromptMessage(nil), prompt.Messages...)
	prompt.Icons = cloneIcons(prompt.Icons)
	server.prompts[prompt.Name] = prompt
	return nil
}
