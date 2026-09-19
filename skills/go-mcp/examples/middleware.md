# Middleware and authorization example

Middleware wraps every MCP method, not only `tools/call`. HTTP and SSE copy
request headers into `request.Meta.Headers`.

```go
func requireToken(next mcp.Handler) mcp.Handler {
	return func(ctx context.Context, request mcp.Request) (any, error) {
		if request.Meta.Headers["Authorization"] != "Bearer example-token" {
			return nil, mcp.ErrUnauthorized
		}
		return next(ctx, request)
	}
}

server, err := mcp.New(
	mcp.ServerInfo{Name: "secured", Version: "1.0.0"},
	mcp.WithMiddleware(requireToken),
)
```

Use `mcp.ErrForbidden` for an authenticated caller without permission. The
core emits safe JSON-RPC errors; HTTP transports also return 401/403 where
applicable. The full runnable sample is
[`example/middleware/main.go`](../../../example/middleware/main.go).
