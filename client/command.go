/*
 * Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 * Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
)

// CommandConfig configures a process-backed stdio transport. A nil Env
// inherits the current process environment; a non-nil Env is passed to the
// child process unchanged. Stderr defaults to os.Stderr and never shares the
// JSON-RPC stdout stream.
type CommandConfig struct {
	StdioConfig
	Dir    string
	Env    []string
	Stderr io.Writer
}

// CommandTransport starts an MCP server as a child process and speaks newline-
// delimited JSON-RPC over its stdin and stdout. The process is started lazily
// by Start and terminated by Close or by cancellation of Start's context.
type CommandTransport struct {
	command string
	args    []string
	config  CommandConfig

	mu            sync.Mutex
	started       bool
	closed        bool
	stdio         *StdioTransport
	process       *exec.Cmd
	stdin         io.Closer
	processCancel context.CancelFunc
	startupErr    error
	ready         chan struct{}
	readyOnce     sync.Once
	closeOnce     sync.Once
}

// NewCommandTransport creates a process-backed stdio transport. It does not
// start the command until the client calls Start.
func NewCommandTransport(command string, args []string, config CommandConfig) (*CommandTransport, error) {
	if command == "" {
		return nil, errors.New("mcp/client: command is required")
	}
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = defaultMaxMessageBytes
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	return &CommandTransport{command: command, args: slices.Clone(args), config: config, ready: make(chan struct{})}, nil
}

// Ready returns a channel closed after the child process has been started (or
// startup has failed).
func (transport *CommandTransport) Ready() <-chan struct{} {
	if transport == nil {
		return nil
	}
	return transport.ready
}

// StartupError returns the error encountered while creating or starting the
// child process. It is meaningful after Ready has been closed.
func (transport *CommandTransport) StartupError() error {
	if transport == nil {
		return ErrClosed
	}
	transport.mu.Lock()
	err := transport.startupErr
	transport.mu.Unlock()
	return err
}

// Start starts the child process and reads its stdout until the process exits
// or the context is cancelled.
func (transport *CommandTransport) Start(ctx context.Context, receiver Receiver) error { //nolint:contextcheck // process lifetime is owned by caller context
	if transport == nil {
		return ErrClosed
	}
	if receiver == nil {
		return errors.New("mcp/client: nil command receiver")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transport.mu.Lock()
	if transport.closed {
		transport.startupErr = ErrClosed
		transport.readyOnce.Do(func() { close(transport.ready) })
		transport.mu.Unlock()
		return ErrClosed
	}
	if transport.started {
		transport.mu.Unlock()
		return ErrStarted
	}
	transport.started = true
	transport.mu.Unlock()
	ready := func() { transport.readyOnce.Do(func() { close(transport.ready) }) }

	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, transport.command, transport.args...) //nolint:gosec // command is explicit caller configuration
	command.Dir = transport.config.Dir
	command.Env = slices.Clone(transport.config.Env)
	command.Stderr = transport.config.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		startErr := fmt.Errorf("mcp/client: create command stdin: %w", err)
		transport.setStartupError(startErr)
		ready()
		return startErr
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		startErr := fmt.Errorf("mcp/client: create command stdout: %w", err)
		transport.setStartupError(startErr)
		ready()
		return startErr
	}
	stdio, err := NewStdioTransport(stdout, stdin, transport.config.StdioConfig)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		transport.setStartupError(err)
		ready()
		return err
	}
	transport.mu.Lock()
	transport.process = command
	transport.stdin = stdin
	transport.processCancel = cancel
	transport.stdio = stdio
	closed := transport.closed
	transport.mu.Unlock()
	if closed {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		transport.setStartupError(ErrClosed)
		ready()
		return ErrClosed
	}
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		transport.setStartupError(ErrClosed)
		ready()
		return ErrClosed
	}
	err = command.Start()
	transport.mu.Unlock()
	if err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		startErr := fmt.Errorf("mcp/client: start command %q: %w", transport.command, err)
		transport.setStartupError(startErr)
		ready()
		return startErr
	}
	ready()

	serveErr := stdio.Start(processCtx, receiver)
	runErr := processCtx.Err()
	cancel()
	waitErr := command.Wait()
	if serveErr != nil && !errors.Is(serveErr, ErrTransportClosed) && !errors.Is(serveErr, context.Canceled) {
		return serveErr
	}
	if waitErr != nil && runErr == nil {
		return fmt.Errorf("mcp/client: command %q exited: %w", transport.command, waitErr)
	}
	if runErr != nil {
		return runErr
	}
	return ErrTransportClosed
}

// Send writes one JSON-RPC message to the child process stdin.
func (transport *CommandTransport) Send(ctx context.Context, payload []byte) ([]byte, error) {
	if transport == nil {
		return nil, ErrClosed
	}
	transport.mu.Lock()
	stdio := transport.stdio
	closed := transport.closed
	transport.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if stdio == nil {
		return nil, ErrNotStarted
	}
	return stdio.Send(ctx, payload)
}

// Close cancels and terminates the child process. It is safe to call multiple
// times and does not close the caller's stderr writer.
func (transport *CommandTransport) Close() error {
	if transport == nil {
		return nil
	}
	transport.closeOnce.Do(func() {
		transport.mu.Lock()
		transport.closed = true
		stdin := transport.stdin
		cancel := transport.processCancel
		stdio := transport.stdio
		transport.mu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		if cancel != nil {
			cancel()
		}
		if stdio != nil {
			_ = stdio.Close()
		}
		transport.readyOnce.Do(func() { close(transport.ready) })
	})
	return nil
}

func (transport *CommandTransport) setStartupError(err error) {
	transport.mu.Lock()
	transport.startupErr = err
	transport.mu.Unlock()
}
