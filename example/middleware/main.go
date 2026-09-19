/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the middleware-protected MCP server example.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
)

type secretInput struct {
	Question string `json:"question" mcp:"description=Question for the protected tool"`
}

// UnmarshalJSON decodes the typed tool input.
func (input *secretInput) UnmarshalJSON(data []byte) error {
	type plain secretInput
	return json.Unmarshal(data, (*plain)(input))
}

type secretOutput struct {
	Answer string `json:"answer"`
}

// MarshalJSON encodes the typed tool output.
func (output *secretOutput) MarshalJSON() ([]byte, error) {
	type plain secretOutput
	return json.Marshal((*plain)(output))
}

func requireToken(next mcp.Handler) mcp.Handler {
	return func(ctx context.Context, request mcp.Request) (any, error) {
		if request.Meta.Headers["Authorization"] != "Bearer example-token" {
			return nil, mcp.ErrUnauthorized
		}
		return next(ctx, request)
	}
}

func main() {
	server, err := mcp.New(
		mcp.ServerInfo{Name: "middleware-example", Version: "1.0.0"},
		mcp.WithMiddleware(requireToken),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "answer", "Answers a protected question", func(_ context.Context, input *secretInput) (*secretOutput, error) {
		return &secretOutput{Answer: "Authorized request: " + input.Question}, nil
	}); err != nil {
		log.Fatal(err)
	}

	transport := mcphttp.NewTransport(mcphttp.Config{Address: ":8080", Path: "/mcp"})
	err = server.Run(transport)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
