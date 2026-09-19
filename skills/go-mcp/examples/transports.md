# Transport server examples

Use these patterns when a task asks for a minimal server for one transport.
The complete runnable versions are in `example/stdio`, `example/http`, and
`example/sse`.

## Stdio

```go
server, err := mcp.New(mcp.ServerInfo{Name: "stdio-example", Version: "1.0.0"})
if err != nil {
	return err
}
transport := stdio.NewTransport(os.Stdin, os.Stdout)
return server.Run(transport)
```

Only JSON-RPC messages may be written to `os.Stdout`; use `log` (stderr) for
diagnostics.

## Streamable HTTP

```go
transport := mcphttp.NewTransport(mcphttp.Config{
	Address: ":8080",
	Path:    "/mcp",
})
return server.Run(transport)
```

## Legacy SSE

```go
transport := sse.NewTransport(sse.Config{
	Address:     ":8080",
	SSEPath:     "/sse",
	MessagePath: "/message",
})
return server.Run(transport)
```

## One HTTP listener for both protocols

```go
transport := mcphttp.NewTransportWithSSE(
	mcphttp.Config{Address: ":8080", Path: "/mcp"},
	sse.Config{SSEPath: "/sse", MessagePath: "/message"},
)
return server.Run(transport)
```
