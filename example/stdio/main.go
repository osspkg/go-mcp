/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the stdio MCP server example.
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/example/model"
	"go.osspkg.com/mcp/stdio"
)

type (
	greetingInput  = model.GreetingInput
	greetingOutput = model.GreetingOutput
)

func main() {
	server, err := mcp.New(mcp.ServerInfo{Name: "stdio-example", Version: "1.0.0"})
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "greet", "Returns a greeting", func(_ context.Context, input *greetingInput) (*greetingOutput, error) {
		return &greetingOutput{Greeting: "Hello, " + input.Name}, nil
	}); err != nil {
		log.Fatal(err)
	}

	err = server.Run(stdio.NewTransport(os.Stdin, os.Stdout))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
