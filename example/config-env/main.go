/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the environment-configured MCP server example.
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/config"
	"go.osspkg.com/mcp/example/model"
)

type (
	configInput  = model.ConfigInput
	configOutput = model.ConfigOutput
)

func main() {
	configuration, err := config.LoadEnv()
	if err != nil {
		log.Fatal(err)
	}
	server, err := mcp.New(mcp.ServerInfo{
		Name:         configuration.Name,
		Version:      configuration.Version,
		Instructions: configuration.Instructions,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterTool(server, "echo", "Echoes a configured request", func(_ context.Context, input *configInput) (*configOutput, error) {
		return &configOutput{Value: input.Value}, nil
	}); err != nil {
		log.Fatal(err)
	}

	transports, err := config.Transports(configuration, server, os.Stdin, os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	err = server.Run(transports...)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
