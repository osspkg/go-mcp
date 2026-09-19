# MCP client example

The client package supports all transports implemented by this repository:

```go
transport, err := client.NewHTTPTransport("http://127.0.0.1:8080/mcp", client.HTTPConfig{})
if err != nil { return err }
mcpClient, err := client.New(transport)
if err != nil { return err }
defer mcpClient.Close()
if err := mcpClient.Start(ctx); err != nil { return err }
if _, err := mcpClient.Initialize(ctx, client.InitializeParams{}); err != nil { return err }
tools, err := mcpClient.ListTools(ctx)
if err != nil { return err }
if len(tools.Tools) != 0 {
    _, err = mcpClient.CallTool(ctx, tools.Tools[0].Name, map[string]any{}, nil)
}
return err
```

Use `NewCommandTransport` to launch a child process, `NewStdioTransport` for
already-open streams, or `NewSSETransport` for a legacy `/sse` endpoint.
`OnRequest` handles server-initiated requests and
`OnNotification` handles progress, logging, and list-changed notifications.
