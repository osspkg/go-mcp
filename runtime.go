/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Transport serves one Server until its context is cancelled.
type Transport interface {
	Serve(ctx context.Context, server *Server) error
}

// Run starts every supplied transport concurrently and owns their coordinated
// shutdown. It stops on os.Interrupt or SIGTERM, waits until all transports
// have returned, and returns context.Canceled for signal-triggered shutdown.
func (server *Server) Run(transports ...Transport) error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	return server.run(ctx, transports...)
}

// RunContext is Run with a caller-owned context. It is useful when the
// application has its own lifecycle or when a server is embedded in another
// process. Cancelling ctx asks every transport to stop.
func (server *Server) RunContext(ctx context.Context, transports ...Transport) error {
	return server.run(ctx, transports...)
}

func (server *Server) run(ctx context.Context, transports ...Transport) error {
	if server == nil {
		return errors.New("mcp: nil server")
	}
	if ctx == nil {
		return errors.New("mcp: nil context")
	}
	if len(transports) == 0 {
		return errors.New("mcp: at least one transport is required")
	}
	for _, transport := range transports {
		if transport == nil {
			return errors.New("mcp: nil transport")
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(transports))
	var group sync.WaitGroup
	for _, transport := range transports {
		group.Add(1)
		go func(item Transport) {
			defer group.Done()
			if err := item.Serve(ctx, server); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}(transport)
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case err := <-errCh:
		cancel()
		<-done
		return err
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	case <-done:
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}
