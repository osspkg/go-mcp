# go-mcp

![Go version](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/go.osspkg.com/mcp.svg)](https://pkg.go.dev/go.osspkg.com/mcp)

`go-mcp` is a stdlib-only Go library for creating Model Context Protocol
servers compatible with MCP `2025-11-25`. It supports newline-delimited stdio,
Streamable HTTP, and legacy HTTP+SSE.

See [DOC.md](DOC.md) for the complete English API reference, lifecycle details,
and developer use cases. A Russian version is available in
[DOC.ru.md](DOC.ru.md).

## Contents

- [Features](#features)
- [Installation](#installation)
- [Documentation](#documentation)
- [Quick start](#quick-start)
- [Middleware and authorization](#middleware-and-authorization)
- [Transports](#transports)
- [Client](#client)
- [Configuration](#configuration)
- [Examples](#examples)
- [Development](#development)
- [License](#license)

## Features

- MCP revision 2025-11-25 and JSON-RPC 2.0.
- Typed tools with JSON Schema generated from Go structs and tags.
- Tool annotations, icons, and output schemas via `RegisterToolWithOptions`.
- Static and dynamic resources, resource templates, and prompts.
- Client features for sampling, roots, elicitation, progress, logging, and
  cancellation.
- Experimental durable Tasks with polling and deferred results.
- Stdio, bidirectional Streamable HTTP, and legacy SSE transports.
- Strict JSON-RPC request validation, bounded stdio framing, and optional request observers.
- Shared middleware pipeline with authorization errors mapped to protocol and
  HTTP status codes.
- Configuration from MCP_* environment variables or flat YAML.
- Graceful signal-aware shutdown through Server.Run.

## Installation

```bash
go get go.osspkg.com/mcp
```

## Documentation

- [English API and developer guide](DOC.md)
- [Russian API and developer guide](DOC.ru.md)
- [go-mcp development skill](skills/go-mcp/SKILL.md)
- [Runnable examples](example/README.md)
- [MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)

## Quick start

### Typed tools

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

## Middleware and authorization

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
`Run` receives a stop signal. It also serves `GET /mcp` as an SSE stream with
bounded event history and `Last-Event-ID` resumption. Set `AllowedOrigins` to
the exact origins accepted by the endpoint; non-empty unlisted origins receive
HTTP 403.

Server-to-client features use `RequestFromContext(ctx)` inside a typed handler:

```go
if request, ok := mcp.RequestFromContext(ctx); ok {
	_ = request.SendProgress(ctx, "job-1", 1, nil, "started")
	_, _ = request.Sample(ctx, mcp.CreateMessageParams{MaxTokens: 64})
}
```

OAuth 2.0 Protected Resource Metadata and OAuth/OIDC discovery documents are
available through `mcp.MarshalMetadata` and the HTTP package's
`NewProtectedResourceMetadataHandler` / `NewAuthorizationServerMetadataHandler`.

### Legacy SSE

```go
transport := sse.NewTransport(sse.Config{
	Address:     ":8080",
	SSEPath:     "/sse",
	MessagePath: "/message",
})
err := server.Run(transport)
```

Each SSE session uses a bounded queue of 16 messages. Expired sessions are
removed by a background cleanup goroutine according to `SessionTTL`; associated
SSE streams are closed, and POST waits for queue capacity or request context
cancellation. Transport shutdown also closes active SSE sessions and stops the
cleanup goroutine.

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

## Client

The `go.osspkg.com/mcp/client` package is a stdlib-only client for all three
transports. It performs the MCP handshake, lists and calls tools, reads
resources, renders prompts, polls Tasks, and dispatches server-initiated
requests to registered handlers.

```go
transport, _ := client.NewHTTPTransport("http://127.0.0.1:8080/mcp", client.HTTPConfig{})
mcpClient, _ := client.New(transport)
defer mcpClient.Close()
_ = mcpClient.Start(ctx)
_, _ = mcpClient.Initialize(ctx, client.InitializeParams{})
tools, _ := mcpClient.ListTools(ctx)
if len(tools.Tools) > 0 {
    _, _ = mcpClient.CallTool(ctx, tools.Tools[0].Name, map[string]any{}, nil)
}
```

Use `client.NewStdioTransport` for child-process servers and
`client.NewSSETransport` for legacy `/sse` endpoints. Register
`OnRequest("sampling/createMessage", ...)`, `OnRequest("roots/list", ...)`, or
`OnRequest("elicitation/create", ...)` before making calls that can trigger
server-to-client requests; use `OnNotification` for progress, logging, and
list-changed events.

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

## Examples

Runnable servers for each transport, middleware, and both configuration
sources are in [`example/`](example/README.md). They use only this module and
the Go standard library.

## Development

The library runtime has no non-standard-library imports. Run the project gates:

```bash
make lint
make tests
go test -race ./...
```

## License

go-mcp is distributed under the [BSD 3-Clause License](LICENSE). See the
license file for the complete copyright and redistribution terms.
