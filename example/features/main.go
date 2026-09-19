/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD-3-Clause license that can be found in the LICENSE file.
 */

// Package main demonstrates capability metadata, tool annotations, icons,
// output schemas, progress, logging, and server-initiated client calls.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
)

type featureInput struct {
	Name string `json:"name" mcp:"description=Name to greet"`
}

func (input *featureInput) UnmarshalJSON(data []byte) error {
	type plain featureInput
	return json.Unmarshal(data, (*plain)(input))
}

type featureOutput struct {
	Greeting string `json:"greeting"`
}

func (output *featureOutput) MarshalJSON() ([]byte, error) {
	type plain featureOutput
	return json.Marshal((*plain)(output))
}

func main() {
	readOnly := true
	server, err := mcp.New(mcp.ServerInfo{
		Name:        "features-example",
		Title:       "MCP Features Example",
		Version:     "1.0.0",
		Description: "Demonstrates MCP 2025-11-25 metadata and notifications.",
		Icons:       []mcp.Icon{{Src: "https://example.test/mcp.svg", MIMEType: "image/svg+xml", Sizes: []string{"any"}}},
	}, mcp.WithCapabilities(map[string]any{
		"experimental": map[string]any{
			"vendor.example": map[string]any{"featureDemo": true},
		},
	}))
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterToolWithOptions(server, "greet", "Returns a greeting with structured output", mcp.ToolOptions{
		Icons:       []mcp.Icon{{Src: "https://example.test/greet.svg", MIMEType: "image/svg+xml"}},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: &readOnly},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"greeting": map[string]any{"type": "string"}},
			"required":   []string{"greeting"},
		},
	}, func(ctx context.Context, input *featureInput) (*featureOutput, error) {
		if request, ok := mcp.RequestFromContext(ctx); ok {
			_ = request.Log(ctx, mcp.LogLevelInfo, map[string]any{"name": input.Name}, "features-example")
			_ = request.SendProgress(ctx, "greet", 1, nil, "completed")
		}
		return &featureOutput{Greeting: "Hello, " + input.Name}, nil
	}); err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "client-roots", "Requests the connected client's roots", func(ctx context.Context, _ *featureInput) (*featureOutput, error) {
		request, ok := mcp.RequestFromContext(ctx)
		if !ok || request.Meta.Call == nil {
			return nil, errors.New("client requests are unavailable on this transport")
		}
		roots, err := request.ListRoots(ctx)
		if err != nil {
			return nil, err
		}
		// Sampling and elicitation use the same request methods:
		// _, _ = request.Sample(ctx, mcp.CreateMessageParams{MaxTokens: 64})
		// _, _ = request.Elicit(ctx, mcp.ElicitRequest{Message: "Continue?", RequestedSchema: map[string]any{"type": "object"}})
		return &featureOutput{Greeting: "Client returned " + strconv.Itoa(len(roots.Roots)) + " root(s)"}, nil
	}); err != nil {
		log.Fatal(err)
	}

	err = server.Run(mcphttp.NewTransport(mcphttp.Config{Address: ":8080", Path: "/mcp"}))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
