/*
 * Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 * Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// The client example connects to a Streamable HTTP MCP server, performs the
// MCP handshake, lists tools, and optionally calls the first tool with an
// empty argument object. Set MCP_ENDPOINT to override the default endpoint.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.osspkg.com/mcp/client"
)

const clientTimeout = 10 * time.Second

func main() {
	endpoint := os.Getenv("MCP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080/mcp"
	}
	transport, err := client.NewHTTPTransport(endpoint, client.HTTPConfig{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	mcpClient, err := client.NewWithConfig(transport, client.ClientConfig{Info: client.Implementation{Name: "go-mcp-example-client", Version: "1.0.0"}})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	defer func() { _ = mcpClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	initialized, err := mcpClient.Initialize(ctx, client.InitializeParams{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("connected to %s %s (protocol %s)\n", initialized.ServerInfo.Name, initialized.ServerInfo.Version, initialized.ProtocolVersion)

	tools, err := mcpClient.ListTools(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Printf("tools: %d\n", len(tools.Tools))
	if len(tools.Tools) == 0 {
		return
	}
	call, err := mcpClient.CallTool(ctx, tools.Tools[0].Name, map[string]any{}, nil)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return
	}
	if call.Task != nil {
		fmt.Printf("task started: %s\n", call.Task.TaskID)
		return
	}
	fmt.Printf("tool result: %+v\n", call.Result)
}
