# go-mcp

`go-mcp` is a stdlib-only Go library for creating Model Context Protocol
servers compatible with MCP `2025-11-25`. It supports newline-delimited stdio,
Streamable HTTP, and legacy HTTP+SSE.

## Install

```bash
go get go.osspkg.com/mcp
```

## Typed tool

Models implement the standard JSON interfaces. `json` tags define the wire
name and whether a field is optional; `mcp` supplies the schema description.

```go
type greetingInput struct {
	Name string `json:"name" mcp:"description=Name to greet"`
}

func (v *greetingInput) UnmarshalJSON(data []byte) error {
	type plain greetingInput
	return json.Unmarshal(data, (*plain)(v))
}

type greetingOutput struct { Greeting string `json:"greeting"` }

func (v *greetingOutput) MarshalJSON() ([]byte, error) {
	type plain greetingOutput
	return json.Marshal((*plain)(v))
}

server, _ := mcp.New(mcp.ServerInfo{Name: "greetings", Version: "1.0.0"})
_ = mcp.RegisterTool(server, "greet", "Returns a greeting",
	func(_ context.Context, in *greetingInput) (*greetingOutput, error) {
		return &greetingOutput{Greeting: "Hello, " + in.Name}, nil
	},
)
```

## Authorization middleware

Middleware runs for every MCP method, including initialization and catalog
listings. For HTTP and SSE, request headers are available through
`request.Meta.Headers`.

```go
server, _ := mcp.New(mcp.ServerInfo{Name: "secured", Version: "1.0.0"},
	mcp.WithMiddleware(func(next mcp.Handler) mcp.Handler {
		return func(ctx context.Context, request mcp.Request) (any, error) {
			if request.Meta.Headers["Authorization"] == "" {
				return nil, mcp.ErrUnauthorized
			}
			return next(ctx, request)
		}
	}),
)
```

## Transports

The transport adapters implement `mcp.Transport` and can be passed directly to
`Server.Run`. Do not write logs to the stdio output writer; it is reserved for
JSON-RPC protocol data.

### Stdio

```go
transport := stdio.NewTransport(os.Stdin, os.Stdout)
err := server.Run(transport)
```

### Streamable HTTP

```go
transport := mcphttp.NewTransport(mcphttp.Config{
	Address: ":8080",
	Path:    "/mcp",
})
err := server.Run(transport)
```

The transport serves `POST /mcp` and performs graceful HTTP shutdown when
`Run` receives a stop signal.

### Legacy SSE

```go
transport := sse.NewTransport(sse.Config{
	Address:     ":8080",
	SSEPath:     "/sse",
	MessagePath: "/message",
})
err := server.Run(transport)
```

For one listener serving both Streamable HTTP and legacy SSE, use
`NewTransportWithSSE`:

```go
transport := mcphttp.NewTransportWithSSE(
	mcphttp.Config{Address: ":8080", Path: "/mcp"},
	sse.Config{SSEPath: "/sse", MessagePath: "/message"},
)
err := server.Run(transport)
```

`Server.Run(transports...)` creates a signal-aware context internally, stops
on `os.Interrupt` or `SIGTERM`, cancels all transports, and waits for every
transport to finish before returning. A signal-triggered shutdown returns
`context.Canceled`, which is normally treated as a clean exit:

```go
err := server.Run(transports...)
if err != nil && !errors.Is(err, context.Canceled) {
	log.Fatal(err)
}
```

Use `Server.RunContext(ctx, transports...)` when the application owns the
parent context and cancellation policy. `RunContext` is also useful for
embedding the server in another process or for tests:

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

err := server.RunContext(ctx, transport)
```

## Configuration

Load exactly one source; do not merge the results of `LoadEnv` and `LoadYAML`.
Environment variables use the `MCP_` prefix, for example `MCP_NAME`,
`MCP_VERSION`, `MCP_ENABLE_HTTP`, and `MCP_HTTP_ADDRESS`.

```go
configuration, err := config.LoadEnv()
if err != nil {
	log.Fatal(err)
}

transports, err := config.Transports(configuration, server, os.Stdin, os.Stdout)
if err != nil {
	log.Fatal(err)
}
err = server.Run(transports...)
```

For YAML configuration, replace the loader with:

```go
configuration, err := config.LoadYAML("server.yaml")
```

The YAML reader intentionally supports only flat scalar values:

```yaml
name: greetings
version: 1.0.0
enable_stdio: false
enable_http: true
http_address: 127.0.0.1:8080
```

Nested YAML, lists, aliases, and unknown keys are rejected rather than parsed
with ambiguous semantics.

## Development

The library runtime has no non-standard-library imports. Run the project gates:

```bash
make lint
make tests
go test -race ./...
```
