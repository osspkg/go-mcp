/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD-3-Clause license that can be found in the LICENSE file.
 */

// Package main demonstrates task-augmented tools and polling.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
)

const defaultJobDelay = 2 * time.Second

type jobInput struct {
	DelayMS int `json:"delayMs,omitempty" mcp:"description=Artificial delay in milliseconds"`
}

func (input *jobInput) UnmarshalJSON(data []byte) error {
	type plain jobInput
	return json.Unmarshal(data, (*plain)(input))
}

type jobOutput struct {
	Status string `json:"status"`
}

func (output *jobOutput) MarshalJSON() ([]byte, error) {
	type plain jobOutput
	return json.Marshal((*plain)(output))
}

func main() {
	server, err := mcp.New(mcp.ServerInfo{Name: "tasks-example", Version: "1.0.0"})
	if err != nil {
		log.Fatal(err)
	}
	if err := mcp.RegisterToolWithOptions(server, "long-job", "Runs a cancellable background job", mcp.ToolOptions{
		TaskSupport: "optional",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: boolPointer(true)},
	}, func(ctx context.Context, input *jobInput) (*jobOutput, error) {
		delay := time.Duration(input.DelayMS) * time.Millisecond
		if delay <= 0 {
			delay = defaultJobDelay
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return &jobOutput{Status: "completed"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}); err != nil {
		log.Fatal(err)
	}

	// A client can request deferred execution with:
	// {"name":"long-job","task":{"ttl":60000},"arguments":{"delayMs":5000}}
	err = server.Run(mcphttp.NewTransport(mcphttp.Config{Address: ":8080", Path: "/mcp"}))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func boolPointer(value bool) *bool { return &value }
