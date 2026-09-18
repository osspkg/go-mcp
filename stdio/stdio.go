/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package stdio serves newline-delimited MCP JSON-RPC over standard streams.
package stdio

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"

	"go.osspkg.com/mcp"
)

// Transport adapts stdio streams to mcp.Transport.
type Transport struct {
	Input  io.Reader
	Output io.Writer
}

// NewTransport creates a stdio transport using the provided streams.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	return &Transport{Input: input, Output: output}
}

// Serve implements mcp.Transport.
func (transport *Transport) Serve(ctx context.Context, server *mcp.Server) error {
	if transport == nil {
		return errors.New("mcp/stdio: nil transport")
	}
	return Serve(ctx, server, transport.Input, transport.Output)
}

// Serve reads requests from input and writes responses to output until the
// context is cancelled or input is closed.
func Serve(ctx context.Context, server *mcp.Server, input io.Reader, output io.Writer) error {
	if server == nil || input == nil || output == nil {
		return errors.New("mcp/stdio: server, input, and output are required")
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	writer := bufio.NewWriter(output)
	defer func() { _ = writer.Flush() }()
	if closer, ok := input.(io.Closer); ok {
		go func() {
			<-serveCtx.Done()
			_ = closer.Close()
		}()
	}
	var mu sync.Mutex
	for scanner.Scan() {
		if err := serveCtx.Err(); err != nil {
			return err
		}
		response, err := server.ServeJSON(serveCtx, scanner.Bytes(), mcp.RequestMeta{Transport: "stdio", Headers: map[string]string{}})
		if err != nil {
			return err
		}
		if response == nil {
			continue
		}
		mu.Lock()
		_, err = writer.Write(append(response, '\n'))
		if err == nil {
			err = writer.Flush()
		}
		mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if contextErr := serveCtx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	}
	return nil
}
