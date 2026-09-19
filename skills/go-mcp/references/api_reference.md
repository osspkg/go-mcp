# go-mcp API reference

Use this reference when implementing protocol behavior or choosing public
APIs. The module path is `go.osspkg.com/mcp` and the supported MCP revision is
`2025-11-25`.

## Package boundaries

| Package | Responsibility |
| --- | --- |
| `mcp` | Server identity, registry, JSON-RPC dispatch, tools, resources, prompts, middleware, runtime composition |
| `mcp/stdio` | Newline-delimited JSON-RPC over `io.Reader`/`io.Writer` |
| `mcp/http` | Streamable HTTP handler and transport at `/mcp` by default |
| `mcp/sse` | Legacy HTTP+SSE transport at `/sse` and `/message` by default |
| `mcp/config` | Flat env/YAML configuration and transport assembly |
| `mcp/client` | Stdlib-only client core plus stdio, Streamable HTTP, and SSE transports |

The root package must not import transport packages. Runtime dependencies are
stdlib-only.

## Server construction and registration

```go
server, err := mcp.New(mcp.ServerInfo{
	Name:         "greetings",
	Version:      "1.0.0",
	Instructions: "Use the greet tool for short greetings.",
})
```

`ServerInfo` may also include a title, description, website URL, and icon
descriptors. `WithCapabilities` adds application-specific capability fields;
the runtime derives standard catalog, logging, and task capabilities.

Use it for experimental or vendor-defined capabilities, or to override a
top-level standard capability:

```go
mcp.WithCapabilities(map[string]any{
	"experimental": map[string]any{"vendor.example": map[string]any{"enabled": true}},
})
```

The map is copied at construction. During `initialize`, built-in capability
defaults are added only for missing top-level keys; supplied values are kept
as-is rather than deep-merged.

Register every catalog item before starting a transport. The first request
seals the registry, after which registration returns `mcp.ErrStarted`.
Duplicate tool, resource, template, and prompt names are rejected.

```go
err := mcp.RegisterTool(server, "greet", "Returns a greeting", handler)
err = server.RegisterResource(mcp.Resource{URI: "memo://welcome", Name: "welcome", Text: "hello"})
err = server.RegisterResourceTemplate(mcp.ResourceTemplate{
	URITemplate: "memo://people/{name}", Name: "person",
	Handler: func(ctx context.Context, request mcp.ResourceRequest) (mcp.Resource, error) {
		return mcp.Resource{URI: request.URI, Name: request.Variables["name"], Text: "hello"}, nil
	},
})
err = server.RegisterPrompt(mcp.Prompt{
	Name: "welcome", Messages: []mcp.PromptMessage{{Role: "user", Text: "hello"}},
})
```

Check each registration error; do not ignore duplicate or validation failures.

## Typed tools and schema

`RegisterTool` infers `In` and `Out` from the handler. Both are pointers to
structs implementing the standard JSON interfaces:

```go
type Input struct {
	Name string `json:"name" mcp:"description=Person name"`
}

func (value *Input) UnmarshalJSON(data []byte) error {
	type plain Input
	return json.Unmarshal(data, (*plain)(value))
}

type Output struct {
	Greeting string `json:"greeting"`
}

func (value *Output) MarshalJSON() ([]byte, error) {
	type plain Output
	return json.Marshal((*plain)(value))
}

err := mcp.RegisterTool(server, "greet", "Returns a greeting",
	func(ctx context.Context, input *Input) (*Output, error) {
		return &Output{Greeting: "Hello, " + input.Name}, nil
	})
```

Schema rules are intentionally small and deterministic:

- the JSON tag name is the wire property name; an empty name falls back to the
  Go field name;
- `json:"-"` omits a field;
- fields without `omitempty` are in `required`;
- `mcp:"description=..."` adds a property description;
- scalar, pointer, struct, slice, and array shapes are supported by the local
  schema generator; unsupported shapes fail at registration;
- tool results become both `structuredContent` and JSON text content.

## Middleware and errors

Middleware runs around every MCP request. The request includes the method,
raw params, transport name, session ID, and copied HTTP headers where the
transport has headers.

```go
func requireToken(next mcp.Handler) mcp.Handler {
	return func(ctx context.Context, request mcp.Request) (any, error) {
		if request.Meta.Headers["Authorization"] != "Bearer example-token" {
			return nil, mcp.ErrUnauthorized
		}
		return next(ctx, request)
	}
}

server, err := mcp.New(info, mcp.WithMiddleware(requireToken))
```

Return `mcp.ErrUnauthorized` or `mcp.ErrForbidden` for auth decisions. The
JSON-RPC response uses safe messages/codes; HTTP transports additionally use
401/403 where the request path can express an HTTP status.

## Protocol surface

The dispatcher implements `initialize`, `ping`, `tools/list`, `tools/call`,
`resources/list`, `resources/templates/list`, `resources/read`, `prompts/list`,
`prompts/get`, subscriptions, logging, and Tasks. Malformed JSON-RPC, invalid
params, unknown methods, and missing catalog entries produce safe JSON-RPC
errors rather than panics or implementation details.

The core also exposes `RequestFromContext` for typed handlers. A request's
`Notify` and `Call` callbacks support progress/logging notifications and
server-initiated sampling, roots, and elicitation requests. Use
`RegisterToolWithOptions` for tool annotations, icons, `outputSchema`, and
task support.

## Client API

`go.osspkg.com/mcp/client` provides `Client`, `Transport`, and constructors
`NewStdioTransport`, `NewHTTPTransport`, and `NewSSETransport`. Call `Start`
with a lifecycle context, then `Initialize`; catalog helpers expose tools,
resources, prompts, logging, and Tasks. `OnRequest` is required for server-
initiated `roots/list`, `sampling/createMessage`, or `elicitation/create`, and
`OnNotification` handles progress, logging, and list-changed notifications.
The package has no authentication or TLS policy and uses only the standard
library.

For transport details, consult the official
[MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
and the repository's [lifecycle reference](lifecycle.md).
