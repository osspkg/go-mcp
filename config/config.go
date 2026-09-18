/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package config loads go-mcp configuration from one explicit source.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReadTimeout        = 15 * time.Second
	defaultWriteTimeout       = 30 * time.Second
	defaultIdleTimeout        = 60 * time.Second
	defaultMaxBodyBytes int64 = 1 << 20
	defaultSessionTTL         = 30 * time.Minute
	defaultMaxSessions        = 256
	initialScanBuffer         = 1024
	maximumScanBuffer         = 64 * 1024
	yamlValueParts            = 2
)

// Config configures the server runtime. Load it from either LoadEnv or
// LoadYAML; callers must not merge sources.
type Config struct {
	Name         string
	Version      string
	Instructions string
	EnableStdio  bool
	EnableHTTP   bool
	EnableSSE    bool
	HTTPAddress  string
	MCPPath      string
	SSEPath      string
	MessagePath  string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	MaxBodyBytes int64
	SessionTTL   time.Duration
	MaxSessions  int
}

// Default returns safe defaults for a local development server.
func Default() Config {
	return Config{
		Name: "mcp-server", Version: "0.0.0", EnableStdio: true,
		HTTPAddress: ":8080", MCPPath: "/mcp", SSEPath: "/sse", MessagePath: "/message",
		ReadTimeout: defaultReadTimeout, WriteTimeout: defaultWriteTimeout, IdleTimeout: defaultIdleTimeout,
		MaxBodyBytes: defaultMaxBodyBytes, SessionTTL: defaultSessionTTL, MaxSessions: defaultMaxSessions,
	}
}

// LoadEnv loads known MCP_* variables from the process environment.
func LoadEnv() (Config, error) {
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.HasPrefix(key, "MCP_") {
			values[strings.TrimPrefix(key, "MCP_")] = value
		}
	}
	return parse(values)
}

// LoadYAML loads a deliberately small YAML subset: one unindented key: scalar
// pair per line, optionally followed by a comment. Lists and nested values are rejected.
func LoadYAML(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("opening configuration: %w", err)
	}
	defer func() { _ = file.Close() }()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScanBuffer), maximumScanBuffer)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(scanner.Text(), " ") || strings.HasPrefix(scanner.Text(), "\t") || strings.HasPrefix(text, "-") {
			return Config{}, fmt.Errorf("line %d: nested values and lists are unsupported", line)
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return Config{}, fmt.Errorf("line %d: expected key: value", line)
		}
		value = strings.TrimSpace(strings.SplitN(value, " #", yamlValueParts)[0])
		if strings.ContainsAny(value, "[]{}") {
			return Config{}, fmt.Errorf("line %d: collections are unsupported", line)
		}
		values[strings.ToUpper(strings.TrimSpace(key))] = strings.Trim(value, "\"'")
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("reading configuration: %w", err)
	}
	return parse(values)
}

func parse(values map[string]string) (Config, error) {
	config := Default()
	for key, value := range values {
		var err error
		switch key {
		case "NAME":
			config.Name = value
		case "VERSION":
			config.Version = value
		case "INSTRUCTIONS":
			config.Instructions = value
		case "ENABLE_STDIO":
			config.EnableStdio, err = strconv.ParseBool(value)
		case "ENABLE_HTTP":
			config.EnableHTTP, err = strconv.ParseBool(value)
		case "ENABLE_SSE":
			config.EnableSSE, err = strconv.ParseBool(value)
		case "HTTP_ADDRESS":
			config.HTTPAddress = value
		case "MCP_PATH":
			config.MCPPath = value
		case "SSE_PATH":
			config.SSEPath = value
		case "MESSAGE_PATH":
			config.MessagePath = value
		case "READ_TIMEOUT":
			config.ReadTimeout, err = time.ParseDuration(value)
		case "WRITE_TIMEOUT":
			config.WriteTimeout, err = time.ParseDuration(value)
		case "IDLE_TIMEOUT":
			config.IdleTimeout, err = time.ParseDuration(value)
		case "MAX_BODY_BYTES":
			config.MaxBodyBytes, err = strconv.ParseInt(value, 10, 64)
		case "SESSION_TTL":
			config.SessionTTL, err = time.ParseDuration(value)
		case "MAX_SESSIONS":
			config.MaxSessions, err = strconv.Atoi(value)
		default:
			return Config{}, fmt.Errorf("unknown configuration key %q", key)
		}
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	if strings.TrimSpace(config.Name) == "" || strings.TrimSpace(config.Version) == "" {
		return Config{}, errors.New("name and version are required")
	}
	if !config.EnableStdio && !config.EnableHTTP && !config.EnableSSE {
		return Config{}, errors.New("at least one transport must be enabled")
	}
	if config.MaxBodyBytes <= 0 || config.MaxSessions <= 0 || config.SessionTTL <= 0 {
		return Config{}, errors.New("body, session TTL, and session limit must be positive")
	}
	return config, nil
}
