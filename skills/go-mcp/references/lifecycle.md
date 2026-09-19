# Runtime lifecycle and shutdown

Read this reference when starting, stopping, embedding, or testing a server.

## Normal application entry point

`Run` owns signal setup and coordinated shutdown:

```go
transports := []mcp.Transport{
	stdio.NewTransport(os.Stdin, os.Stdout),
	// mcphttp.NewTransport(...),
}

if err := server.Run(transports...); err != nil && !errors.Is(err, context.Canceled) {
	log.Fatal(err)
}
```

Internally it uses `signal.NotifyContext(context.Background(), os.Interrupt,
syscall.SIGTERM)`, starts each transport concurrently, and derives a child
context for all of them. A signal cancels the child context, every transport is
allowed to finish, and `Run` waits for all transport goroutines before it
returns `context.Canceled`.

If a transport returns a non-cancellation error, `Run` cancels the remaining
transports, waits for them, and returns that error. Validate all transport
values before starting; nil or empty transport lists are errors.

## Embedded lifecycle

Use `RunContext` when the host owns cancellation, a deadline, or test control:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

err := server.RunContext(ctx, transport)
```

`RunContext` keeps the same concurrent startup, sibling cancellation, and wait
semantics as `Run`; it does not install process-global signal handlers.

## Transport-specific behavior

- Stdio reads newline-delimited JSON-RPC and flushes every response. On
  cancellation it closes an input that implements `io.Closer`; a non-closable
  reader blocked in `Read` cannot be interrupted by context alone.
- Streamable HTTP and legacy SSE call `http.Server.Shutdown` with a five-second
  graceful-shutdown budget after their context is cancelled. New connections
  stop while active requests are given time to complete.
- An SSE client disconnect cancels the request context and removes its session.
- Transport shutdown closes all remaining SSE sessions and stops their cleanup
  goroutine. Call `(*sse.Handler).Close` when a standalone handler is removed
  from an application-owned HTTP server.

Do not call `http.ListenAndServe` in a sample that claims to demonstrate the
managed lifecycle; use the transport adapter and `Run`. Direct `Serve` or
handler APIs remain useful when an application already owns an `http.Server`.
