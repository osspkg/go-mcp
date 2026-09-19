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

const (
	initialScanBuffer = 1024
	maximumScanBuffer = 1 << 20
)

// Config configures stdio framing limits.
type Config struct {
	MaxMessageBytes int
}

// DefaultConfig returns safe stdio framing defaults.
func DefaultConfig() Config { return Config{MaxMessageBytes: maximumScanBuffer} }

// Transport adapts stdio streams to mcp.Transport.
type Transport struct {
	Input  io.Reader
	Output io.Writer
	Config Config
}

// NewTransport creates a stdio transport using the provided streams.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	return NewTransportWithConfig(input, output, DefaultConfig())
}

// NewTransportWithConfig creates a stdio transport with explicit framing limits.
func NewTransportWithConfig(input io.Reader, output io.Writer, config Config) *Transport {
	return &Transport{Input: input, Output: output, Config: config}
}

// Serve implements mcp.Transport.
func (transport *Transport) Serve(ctx context.Context, server *mcp.Server) error {
	if transport == nil {
		return errors.New("mcp/stdio: nil transport")
	}
	return ServeWithConfig(ctx, server, transport.Input, transport.Output, transport.Config)
}

// Serve reads requests from input and writes responses to output until the
// context is cancelled or input is closed.
func Serve(ctx context.Context, server *mcp.Server, input io.Reader, output io.Writer) error {
	return ServeWithConfig(ctx, server, input, output, DefaultConfig())
}

// ServeWithConfig serves stdio requests with explicit framing limits.
func ServeWithConfig(ctx context.Context, server *mcp.Server, input io.Reader, output io.Writer, config Config) error {
	if server == nil || input == nil || output == nil {
		return errors.New("mcp/stdio: server, input, and output are required")
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(input)
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = maximumScanBuffer
	}
	initialBuffer := initialScanBuffer
	if config.MaxMessageBytes < initialBuffer {
		initialBuffer = config.MaxMessageBytes
	}
	scanner.Buffer(make([]byte, initialBuffer), config.MaxMessageBytes)
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
