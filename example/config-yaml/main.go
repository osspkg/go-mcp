/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main runs the YAML-configured MCP server example.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"

	"go.osspkg.com/mcp"
	"go.osspkg.com/mcp/config"
)

type configInput struct {
	Value string `json:"value" mcp:"description=Value to echo"`
}

// UnmarshalJSON decodes the typed tool input.
func (input *configInput) UnmarshalJSON(data []byte) error {
	type plain configInput
	return json.Unmarshal(data, (*plain)(input))
}

type configOutput struct {
	Value string `json:"value"`
}

// MarshalJSON encodes the typed tool output.
func (output *configOutput) MarshalJSON() ([]byte, error) {
	type plain configOutput
	return json.Marshal((*plain)(output))
}

func main() {
	path := "server.yaml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	configuration, err := config.LoadYAML(path)
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
