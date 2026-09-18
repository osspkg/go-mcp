/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package config

import (
	"errors"
	"io"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
	"go.osspkg.com/mcp/sse"
	"go.osspkg.com/mcp/stdio"
)

const transportCapacity = 2

// Transports builds the enabled transports for Server.Run. HTTP and legacy
// SSE share one listener when both are enabled.
func Transports(configuration Config, server *mcp.Server, input io.Reader, output io.Writer) ([]mcp.Transport, error) {
	if server == nil {
		return nil, errors.New("mcp/config: nil server")
	}
	transports := make([]mcp.Transport, 0, transportCapacity)
	if configuration.EnableStdio {
		if input == nil || output == nil {
			return nil, errors.New("mcp/config: stdio streams are required")
		}
		transports = append(transports, stdio.NewTransport(input, output))
	}
	if configuration.EnableHTTP || configuration.EnableSSE {
		httpConfig := mcphttp.Config{
			Address:      configuration.HTTPAddress,
			Path:         configuration.MCPPath,
			MaxBodyBytes: configuration.MaxBodyBytes,
			SessionTTL:   configuration.SessionTTL,
			MaxSessions:  configuration.MaxSessions,
			ReadTimeout:  configuration.ReadTimeout,
			WriteTimeout: configuration.WriteTimeout,
			IdleTimeout:  configuration.IdleTimeout,
		}
		switch {
		case configuration.EnableHTTP && configuration.EnableSSE:
			legacyConfig := sse.Config{
				Address:      configuration.HTTPAddress,
				SSEPath:      configuration.SSEPath,
				MessagePath:  configuration.MessagePath,
				MaxBodyBytes: configuration.MaxBodyBytes,
				SessionTTL:   configuration.SessionTTL,
				MaxSessions:  configuration.MaxSessions,
				ReadTimeout:  configuration.ReadTimeout,
				WriteTimeout: configuration.WriteTimeout,
				IdleTimeout:  configuration.IdleTimeout,
			}
			transports = append(transports, mcphttp.NewTransportWithSSE(httpConfig, legacyConfig))
		case configuration.EnableHTTP:
			transports = append(transports, mcphttp.NewTransport(httpConfig))
		default:
			transports = append(transports, sse.NewTransport(sse.Config{
				Address:      configuration.HTTPAddress,
				SSEPath:      configuration.SSEPath,
				MessagePath:  configuration.MessagePath,
				MaxBodyBytes: configuration.MaxBodyBytes,
				SessionTTL:   configuration.SessionTTL,
				MaxSessions:  configuration.MaxSessions,
				ReadTimeout:  configuration.ReadTimeout,
				WriteTimeout: configuration.WriteTimeout,
				IdleTimeout:  configuration.IdleTimeout,
			}))
		}
	}
	if len(transports) == 0 {
		return nil, errors.New("mcp/config: no transports enabled")
	}
	return transports, nil
}
