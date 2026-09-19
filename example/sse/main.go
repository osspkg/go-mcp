/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the legacy SSE MCP server example.
package main

import (
	"context"
	"errors"
	"log"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/example/model"
	"go.osspkg.com/mcp/sse"
)

type (
	echoInput  = model.EchoInput
	echoOutput = model.EchoOutput
)

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
