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

Run stdio directly with process stdin/stdout. Do not write logs to the output
writer; it is reserved for protocol data.

```go
err := stdio.Serve(ctx, server, os.Stdin, os.Stdout)
```

Use the HTTP handler at `/mcp` with `net/http`:

```go
handler, _ := mcphttp.NewHandler(server, mcphttp.DefaultConfig())
err := http.ListenAndServe(":8080", handler)
```

For legacy clients, mount the SSE handler; it serves `/sse` and `/message`.

```go
handler, _ := sse.NewHandler(server, sse.DefaultConfig())
err := http.ListenAndServe(":8080", handler)
```

`Server.Run(ctx, transports...)` starts implementations of `mcp.Transport`
concurrently. Use `http.NewTransportWithSSE` when `/mcp`, `/sse`, and
`/message` must share one listener.

## Configuration

Load exactly one source. Environment variables use the `MCP_` prefix, for
example `MCP_NAME`, `MCP_VERSION`, `MCP_ENABLE_HTTP`, and `MCP_HTTP_ADDRESS`.

```go
fromEnv, err := config.LoadEnv()
fromFile, err := config.LoadYAML("server.yaml")

transports, err := config.Transports(fromEnv, server, os.Stdin, os.Stdout)
err = server.Run(ctx, transports...)
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
