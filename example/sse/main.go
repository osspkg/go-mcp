/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the legacy SSE MCP server example.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/sse"
)

type echoInput struct {
	Message string `json:"message" mcp:"description=Message to echo"`
}

// UnmarshalJSON decodes the typed tool input.
func (input *echoInput) UnmarshalJSON(data []byte) error {
	type plain echoInput
	return json.Unmarshal(data, (*plain)(input))
}

type echoOutput struct {
	Message string `json:"message"`
}

// MarshalJSON encodes the typed tool output.
func (output *echoOutput) MarshalJSON() ([]byte, error) {
	type plain echoOutput
	return json.Marshal((*plain)(output))
}

func main() {
	server, err := mcp.New(mcp.ServerInfo{Name: "sse-example", Version: "1.0.0"})
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "echo", "Echoes a message", func(_ context.Context, input *echoInput) (*echoOutput, error) {
		return &echoOutput{Message: input.Message}, nil
	}); err != nil {
		log.Fatal(err)
	}

	transport := sse.NewTransport(sse.Config{
		Address:     ":8080",
		SSEPath:     "/sse",
		MessagePath: "/message",
	})
	err = server.Run(transport)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
