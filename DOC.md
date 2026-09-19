# go-mcp: API and developer guide

Document status: current for MCP revision 2025-11-25.

Module: go.osspkg.com/mcp

go-mcp is a stdlib-only Model Context Protocol server library with one core and
three transports: newline-delimited JSON over stdio, Streamable HTTP, and legacy
SSE. Register tools, resources, and prompts, add middleware, and run all
enabled transports through Run.

## Contents

1. [Purpose and scope](#1-purpose-and-scope)
2. [Architecture](#2-architecture)
3. [Quick start](#3-quick-start)
4. [Core mcp API](#4-core-mcp-api)
5. [Typed tools and JSON Schema](#5-typed-tools-and-json-schema)
6. [Resources and prompts](#6-resources-and-prompts)
7. [Middleware and authorization](#7-middleware-and-authorization)
8. [JSON-RPC and MCP methods](#8-json-rpc-and-mcp-methods)
9. [Transports](#9-transports)
10. [Configuration](#10-configuration)
11. [Lifecycle and shutdown](#11-lifecycle-and-shutdown)
12. [Developer use cases](#12-developer-use-cases)
13. [Errors and security](#13-errors-and-security)
14. [Testing and quality](#14-testing-and-quality)
15. [Related material and changes](#15-related-material-and-changes)

## 1. Purpose and scope

Use go-mcp when you need to embed an MCP server in a CLI, local agent, or HTTP
service without runtime dependencies outside the Go standard library. The
go.mod file contains no third-party runtime modules; linters and development
tools remain development-only.

Supported features:

- MCP 2025-11-25;
- JSON-RPC 2.0;
- tools, resources, resource templates, and prompts;
- stdio, Streamable HTTP, and legacy SSE;
- one middleware pipeline for every MCP call;
- configuration from MCP_* variables or flat YAML.

The library does not provide a user store, JWT/OAuth provider, CORS policy, TLS
termination, or business logic. Keep those concerns in your application or
reverse proxy.

## 2. Architecture

Public packages are separated by responsibility:

| Package | Responsibility |
| --- | --- |
| mcp | server, registry, JSON-RPC, typed tools, middleware, MCP catalog |
| mcp/config | configuration loading and validation |
| mcp/stdio | newline-delimited JSON transport |
| mcp/http | Streamable HTTP and legacy SSE on one listener |
| mcp/sse | legacy SSE transport |

The mcp core does not import transport packages. A transport receives the
server through this interface:

~~~go
type Transport interface {
    Serve(context.Context, *mcp.Server) error
}
~~~

Request flow:

1. the transport accepts bytes and creates an mcp.Request;
2. the server runs middleware with the request context;
3. JSON-RPC dispatch selects an MCP method;
4. the registry calls a tool, resource, or prompt handler;
5. the transport serializes the response and manages the connection or session.

## 3. Quick start

The following is a minimal typed-tool server over stdio. Tool models are
pointers to application structures and implement json.Unmarshaler/json.Marshaler
(normally through explicit methods backed by encoding/json).

~~~go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"

    "go.osspkg.com/mcp"
    "go.osspkg.com/mcp/stdio"
)

type AddInput struct {
    A int `json:"a" mcp:"description=First addend"`
    B int `json:"b" mcp:"description=Second addend"`
}

func (v *AddInput) UnmarshalJSON(data []byte) error {
    type alias AddInput
    var value alias
    if err := json.Unmarshal(data, &value); err != nil {
        return err
    }
    *v = AddInput(value)
    return nil
}

type AddOutput struct {
    Result int `json:"result"`
}

func (v *AddOutput) MarshalJSON() ([]byte, error) {
    type alias AddOutput
    return json.Marshal(alias(*v))
}

func main() {
    server, err := mcp.New(mcp.ServerInfo{Name: "calculator", Version: "1.0.0"})
    if err != nil {
        panic(err)
    }
    if err := mcp.RegisterTool(server, "add", "Adds two numbers",
        func(_ context.Context, input *AddInput) (*AddOutput, error) {
            return &AddOutput{Result: input.A + input.B}, nil
        }); err != nil {
        panic(err)
    }

    transport := stdio.NewTransport(os.Stdin, os.Stdout)
    if err := server.Run(transport); err != nil {
        panic(fmt.Errorf("mcp server: %w", err))
    }
}
~~~

Run creates a context that is cancelled by os.Interrupt or syscall.SIGTERM. If
the caller owns the lifecycle, use RunContext instead.

## 4. Core mcp API

### Creating a server

~~~go
type ServerInfo struct {
    Name         string
    Version      string
    Instructions string
}

type Option func(*Server) error

func New(info ServerInfo, options ...Option) (*Server, error)
func WithMiddleware(middleware ...Middleware) Option
~~~

Name and Version are required. Instructions are returned to the client during
initialize. WithMiddleware preserves argument order: the first middleware in
the list receives the request first.

New returns an error for empty required fields, a nil option, or nil middleware.
The server does not create a transport by itself; pass transports to Run.

### Requests and handlers

~~~go
type RequestMeta struct {
    Transport string
    Headers   map[string]string
    SessionID string
}

type Request struct {
    Method string
    Params json.RawMessage
    Meta   RequestMeta
}

type Handler func(context.Context, Request) (any, error)
type Middleware func(Handler) Handler
~~~

RequestMeta.Transport is stdio, http, or sse. HTTP and SSE pass a copy of
incoming headers and the MCP session identifier. Middleware can read the method
from Request.Method and cancellation from context.Context.

### Direct JSON-RPC calls

~~~go
func (s *Server) ServeJSON(
    ctx context.Context,
    payload []byte,
    meta RequestMeta,
) ([]byte, error)
~~~

Use ServeJSON for a custom transport or tests. It parses one JSON-RPC request
and applies the same registry and middleware pipeline as the built-in
transports. Invalid JSON produces a JSON-RPC parse error; unknown methods,
invalid parameters, and handler errors are encoded as safe protocol errors.

### Catalog and registry sealing

Registration methods:

~~~go
func (s *Server) RegisterResource(resource Resource) error
func (s *Server) RegisterResourceTemplate(template ResourceTemplate) error
func (s *Server) RegisterPrompt(prompt Prompt) error
~~~

Tool and prompt names, and resource URIs, must be unique within their
categories. The first request seals the registry. Later registrations return
ErrStarted. This makes concurrent serving safe and keeps */list responses
stable.

## 5. Typed tools and JSON Schema

The primary tool API is a generic function:

~~~go
type ToolHandler[In json.Unmarshaler, Out json.Marshaler] func(
    context.Context,
    In,
) (Out, error)

func RegisterTool[In json.Unmarshaler, Out json.Marshaler](
    server *Server,
    name string,
    description string,
    handler ToolHandler[In, Out],
) error
~~~

In and Out must be pointers to application structures. The input must provide
UnmarshalJSON and the output must provide MarshalJSON. This makes the contract
explicit and lets registration validate the JSON codec.

JSON Schema is built from fields and tags:

| Entry | Result |
| --- | --- |
| `json:"user_id"` | property name user_id |
| `json:"name,omitempty"` | optional property name |
| `json:"name"` | required property name |
| `json:"-"` | field is omitted |
| `mcp:"description=..."` | property description |

Scalar types, pointers, nested structures, slices, and arrays are supported.
Unsupported types and malformed tags fail during registration.

A successful tools/call response contains the serialized output in
structuredContent and in JSON text content. Input decoding and handler errors
become a tool result with isError: true; internal implementation details are
not exposed in the text.

## 6. Resources and prompts

### Static and dynamic resources

~~~go
type Resource struct {
    URI         string
    Name        string
    Description string
    MIMEType    string
    Text        string
}

type ResourceRequest struct {
    URI       string
    Variables map[string]string
}

type ResourceHandler func(context.Context, ResourceRequest) (Resource, error)
~~~

Register a static resource with Text populated. For computed content, use a
ResourceTemplate:

~~~go
type ResourceTemplate struct {
    URITemplate string
    Name        string
    Description string
    MIMEType    string
    Handler     ResourceHandler
}
~~~

Templates use slash-separated segments and named placeholders such as
file:///{path}; one placeholder matches one URI segment. During resources/read,
the server passes extracted values in Variables. URI and Name are required, and
the template must have a Handler.

### Prompts

~~~go
type PromptMessage struct {
    Role string
    Text string
}

type PromptArgument struct {
    Name        string
    Description string
    Required    bool
}

type PromptHandler func(context.Context, map[string]string) ([]PromptMessage, error)

type Prompt struct {
    Name        string
    Description string
    Arguments   []PromptArgument
    Messages    []PromptMessage
    Handler     PromptHandler
}
~~~

For a static prompt, set Messages. For a dynamic prompt, set Handler; the
callback receives prompts/get arguments and can return different messages per
request. A prompt must have either Messages or Handler.

## 7. Middleware and authorization

Middleware runs for initialize, discovery methods, and catalog calls on every
transport. A bearer-token check can look like this:

~~~go
func requireToken(next mcp.Handler) mcp.Handler {
    return func(ctx context.Context, req mcp.Request) (any, error) {
        if req.Meta.Headers["Authorization"] != "Bearer secret" {
            return nil, mcp.ErrUnauthorized
        }
        return next(ctx, req)
    }
}

server, err := mcp.New(info, mcp.WithMiddleware(requireToken))
~~~

You can also check req.Meta.Transport, req.Meta.SessionID, and application
headers. Middleware must respect context cancellation and must not write the
response itself.

| Handler error | JSON-RPC code | HTTP status |
| --- | ---: | ---: |
| mcp.ErrUnauthorized | -32001 | 401 |
| mcp.ErrForbidden | -32003 | 403 |
| other error | -32603 or safe tool error | 200/500 depending on context |

For stdio, these remain JSON-RPC errors. HTTP and SSE add the corresponding
status without disclosing secrets.

## 8. JSON-RPC and MCP methods

Supported methods:

| Method | Purpose |
| --- | --- |
| initialize | handshake, protocol version, capabilities, and server info |
| ping | availability check |
| tools/list | registered tools and input schemas |
| tools/call | invoke a typed tool |
| resources/list | static resources |
| resources/templates/list | URI templates |
| resources/read | read a static or dynamic resource |
| prompts/list | prompts and arguments |
| prompts/get | retrieve prompt messages |

The initialize response declares protocol version 2025-11-25 and tools,
resources, and prompts capabilities. Unknown methods receive -32601, invalid
parameters receive -32602, and malformed JSON receives -32700. A JSON-RPC
notification without an id does not require a response.

## 9. Transports

### Stdio

Package go.osspkg.com/mcp/stdio:

~~~go
transport := stdio.NewTransport(os.Stdin, os.Stdout)
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

Each stdin line is one JSON-RPC object, and each stdout line is one response.
The scanner limit is 1 MiB. Protocol data goes only to stdout; write
diagnostics to stderr. On context cancellation, the transport closes the input
when it implements io.Closer.

If you do not need to keep the transport value, use:

~~~go
err := stdio.Serve(ctx, server, os.Stdin, os.Stdout)
~~~

### Streamable HTTP

Package go.osspkg.com/mcp/http (usually imported as mcphttp):

~~~go
transport := mcphttp.NewTransport(mcphttp.Config{
    Address: ":8080",
    Path:    "/mcp",
})
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

POST /mcp accepts application/json. initialize creates an Mcp-Session-Id;
subsequent requests must send the same header. The transport supports body
limits, session TTL and maximum sessions, read/write/idle timeouts, and graceful
shutdown. CORS is not enabled by default.

To mount the handler in an existing mux:

~~~go
handler, err := mcphttp.NewHandler(server, mcphttp.DefaultConfig())
if err != nil {
    log.Fatal(err)
}
http.Handle("/mcp", handler)
~~~

For a standard net/http.Server, use mcphttp.Server:

~~~go
srv := mcphttp.Server(ctx, mcphttp.ServerConfig{
    Address: ":8080",
    Handler: handler,
})
err := srv.ListenAndServe()
~~~

### Legacy SSE

Package go.osspkg.com/mcp/sse keeps compatibility with clients that require
GET /sse and POST /message:

~~~go
transport := sse.NewTransport(sse.Config{
    SSEPath:     "/sse",
    MessagePath: "/message",
})
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

GET /sse returns text/event-stream and an endpoint event with the message URL.
POST /message?sessionId=... places the response in the SSE queue. The session
is removed when the client disconnects.

HTTP can serve Streamable HTTP and legacy SSE on one listener:

~~~go
transport := mcphttp.NewTransportWithSSE(
    mcphttp.Config{Address: ":8080", Path: "/mcp"},
    sse.Config{SSEPath: "/sse", MessagePath: "/message"},
)
~~~

Each SSE session has a buffered queue of 16 messages. If it is full, POST waits
until the SSE reader consumes an item or the request context is cancelled.
A background cleanup goroutine checks the session TTL, removes expired
sessions, and closes their SSE streams. It starts when the first session is
created and stops after the last session is removed.

Full HTTP transport settings:

~~~go
type Config struct {
    Address      string
    Path         string
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func DefaultConfig() Config
func NewTransport(config Config) *Transport
func NewTransportWithSSE(config Config, legacy sse.Config) *Transport
func NewHandler(server *mcp.Server, config Config) (*Handler, error)
~~~

SSE Config has the same fields and additionally SSEPath and MessagePath:

~~~go
type Config struct {
    Address      string
    SSEPath      string
    MessagePath  string
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func DefaultConfig() Config
func NewTransport(config Config) *Transport
func NewHandler(server *mcp.Server, config Config) (*Handler, error)
~~~

Both HTTP packages export the same managed-listener helper:

~~~go
import stdhttp "net/http"

type ServerConfig struct {
    Address      string
    Handler      stdhttp.Handler
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func Server(ctx context.Context, config ServerConfig) *stdhttp.Server
~~~

Server returns a regular net/http.Server. You call ListenAndServe; the library
calls Shutdown when the context is cancelled.

## 10. Configuration

Package go.osspkg.com/mcp/config supports two mutually exclusive sources.
Choose one loader and pass the result to Transports.

~~~go
cfg, err := config.LoadEnv()
// or: cfg, err := config.LoadYAML("mcp.yaml")
if err != nil {
    log.Fatal(err)
}
transports, err := config.Transports(cfg, server, os.Stdin, os.Stdout)
~~~

Key config.Config fields:

| Field | Purpose |
| --- | --- |
| Name, Version, Instructions | identity and handshake |
| EnableStdio, EnableHTTP, EnableSSE | enable transports |
| HTTPAddress | HTTP listener address |
| MCPPath, SSEPath, MessagePath | URL paths |
| ReadTimeout, WriteTimeout, IdleTimeout | network timeouts |
| MaxBodyBytes | JSON body limit |
| SessionTTL, MaxSessions | session settings |

config.Default() enables stdio and sets :8080, /mcp, /sse, /message, a 1 MiB
body limit, a 30-minute TTL, and a maximum of 256 sessions.

Full configuration signature:

~~~go
type Config struct {
    Name         string
    Version      string
    Instructions string
    EnableStdio  bool
    EnableHTTP   bool
    EnableSSE    bool
    HTTPAddress  string
    MCPPath      string
    SSEPath      string
    MessagePath  string
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
}

func Default() Config
func LoadEnv() (Config, error)
func LoadYAML(path string) (Config, error)
func Transports(
    configuration Config,
    server *mcp.Server,
    input io.Reader,
    output io.Writer,
) ([]mcp.Transport, error)
~~~

When stdio is enabled, input and output must be non-nil. When HTTP and SSE are
both enabled, Transports creates one HTTP listener with both endpoint sets.

### Environment

LoadEnv reads only known MCP_* variables:

~~~text
MCP_NAME=calculator
MCP_VERSION=1.0.0
MCP_ENABLE_STDIO=false
MCP_ENABLE_HTTP=true
MCP_HTTP_ADDRESS=:9090
MCP_MCP_PATH=/mcp
MCP_MAX_BODY_BYTES=1048576
MCP_SESSION_TTL=30m
~~~

An unknown key, an empty required value, or an invalid boolean, integer, or
duration returns an error.

### YAML

LoadYAML intentionally accepts only flat scalar entries:

~~~yaml
name: calculator
version: "1.0.0"
enable_stdio: false
enable_http: true
http_address: ":9090"
session_ttl: 30m
~~~

Nested maps, lists, anchors, and unknown fields are rejected. This keeps
validation deterministic and prevents ambiguous configuration merging.

## 11. Lifecycle and shutdown

### Default signal handling

Server.Run(transports ...) encapsulates:

~~~go
ctx, stop := signal.NotifyContext(
    context.Background(),
    os.Interrupt,
    syscall.SIGTERM,
)
defer stop()
return server.run(ctx, transports...)
~~~

Ctrl-C or SIGTERM cancels the shared context. Each transport then stops
accepting new requests, closes active sessions, and returns. Do not install a
second signal handler around Run unless your application needs different
behavior.

### Caller-owned context

Use RunContext for embedding and tests:

~~~go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

go func() {
    _ = server.RunContext(ctx, transport)
}()

// ... application work ...
cancel() // graceful shutdown
~~~

RunContext does not install a signal handler and follows the caller's
cancellation policy. For the HTTP helper Server(ctx, config), shutdown starts
automatically and the graceful-shutdown period is limited to five seconds.

## 12. Developer use cases

### 12.1 Local CLI agent

Use stdio when the MCP client launches your binary as a child process. Keep
EnableStdio=true, write logs only to stderr, and register small idempotent
tools. This mode needs no open port and uses the process boundary as its
access-control boundary.

### 12.2 Remote HTTP service

Enable Streamable HTTP, set an explicit HTTPAddress, MaxBodyBytes, timeouts, and
authorization middleware. Terminate TLS and apply rate limiting at a reverse
proxy; validate bearer or session claims in middleware and handlers.

### 12.3 Legacy SSE client

Create NewTransportWithSSE when some clients still require SSE. Keep /mcp, /sse,
and /message distinct. Set MaxSessions and SessionTTL so disconnected browser
clients cannot retain resources indefinitely.

### 12.4 Dynamic data

Register ResourceTemplate for files, documents, or tenant-scoped data. Extract
only variables matched by the template, and enforce access in middleware and
inside the handler.

### 12.5 Contextual prompts

Use Prompt.Handler when messages depend on arguments, the user, or current
application state. Do not put secrets in PromptMessage: the prompt is returned
to the MCP client.

### 12.6 Embedding in an existing server

Use NewHandler and register it in your own http.ServeMux when the application
already owns the listener, TLS, and health endpoints. For full lifecycle
control, pass your context through RunContext or use mcphttp.Server(ctx, ...).

## 13. Errors and security

Public sentinel errors:

~~~go
var (
    ErrUnauthorized = errors.New("unauthorized")
    ErrForbidden    = errors.New("forbidden")
    ErrStarted      = errors.New("server already started")
)
~~~

Recommendations:

- limit HTTP and SSE message bodies;
- set finite read, write, and idle timeouts;
- authorize before reading sensitive resources;
- never return stack traces or tokens from handler errors;
- do not enable CORS without an explicit origin list;
- validate URI-template paths and tenant values;
- never write diagnostics to stdio stdout;
- pass ctx to external operations and handle cancellation immediately.

## 14. Testing and quality

The repository provides:

~~~text
make lint
make tests
go test -race ./...
~~~

make lint checks formatting, vet, and static analysis. make tests runs unit and
integration tests. Race testing is required for changes to the registry,
sessions, shutdown, or concurrent handlers.

For your own tool, test:

- typed-tool schemas and required fields;
- duplicate names and registration after startup;
- middleware over stdio, HTTP, and SSE;
- malformed JSON-RPC, unknown methods, and invalid parameters;
- 401/403, body limits, and invalid session IDs;
- handler context cancellation and graceful shutdown;
- content types, the SSE endpoint event, and client disconnects.

## 15. Related material and changes

- [README.md](README.md) — concise overview and minimal examples.
- [DOC.ru.md](DOC.ru.md) — Russian API reference.
- [example/README.md](example/README.md) — runnable stdio, HTTP, SSE,
  middleware, and configuration examples.
- [AGENTS.md](AGENTS.md) — development rules and required checks.
- [skills/go-mcp/SKILL.md](skills/go-mcp/SKILL.md) — AI-assisted development
  workflow and API/lifecycle/configuration references.
- [MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
  — normative transport behavior.
- [LICENSE](LICENSE) — BSD 3-Clause License.

### Documentation changelog

| Date | Change |
| --- | --- |
| 2026-09-19 | Added the English API reference and developer use cases. |
| 2026-09-19 | Added background cleanup for expired SSE sessions and their streams. |
