package mcp

import (
	"context"
	"errors"
	"sync"
)

// Transport serves one Server until its context is cancelled.
type Transport interface {
	Serve(context.Context, *Server) error
}

// Run starts every supplied transport concurrently and returns the first
// transport failure. Cancelling ctx asks every transport to stop.
func (server *Server) Run(ctx context.Context, transports ...Transport) error {
	if server == nil {
		return errors.New("mcp: nil server")
	}
	if len(transports) == 0 {
		return errors.New("mcp: at least one transport is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(transports))
	var group sync.WaitGroup
	for _, transport := range transports {
		if transport == nil {
			return errors.New("mcp: nil transport")
		}
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
		return err
	case <-ctx.Done():
		<-done
		return ctx.Err()
	case <-done:
		return nil
	}
}
