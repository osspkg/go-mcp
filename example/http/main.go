/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the Streamable HTTP MCP server example.
package main

import (
	"context"
	"errors"
	"log"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/example/model"
	mcphttp "go.osspkg.com/mcp/http"
)

type (
	timeInput  = model.TimeInput
	timeOutput = model.TimeOutput
)

func main() {
	server, err := mcp.New(mcp.ServerInfo{Name: "http-example", Version: "1.0.0"})
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "current-time", "Returns a time message", func(_ context.Context, input *timeInput) (*timeOutput, error) {
		if input.Timezone == "" {
			input.Timezone = "UTC"
		}
		return &timeOutput{Message: "The current time zone is " + input.Timezone}, nil
	}); err != nil {
		log.Fatal(err)
	}

	transport := mcphttp.NewTransport(mcphttp.Config{Address: ":8080", Path: "/mcp"})
	err = server.Run(transport)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
