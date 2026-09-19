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
   - [Registration APIs and LLM interaction](#registration-apis-and-llm-interaction)
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
func WithRequestObserver(observers ...RequestObserver) Option
~~~

Tool and prompt names, and resource URIs, must be unique within their
categories. The first request seals the registry. Later registrations return
ErrStarted. This makes concurrent serving safe and keeps */list responses
stable.

### Registration APIs and LLM interaction

Registration adds a capability to the server catalog. It does not call an LLM
and it does not expose a Go function directly to the model. The MCP client
connects to the server, discovers the catalog through JSON-RPC, and decides
which catalog entries to make available to the model.

| API | What you register | Discovery method | Request method | Best for |
| --- | --- | --- | --- | --- |
| `RegisterResource` | A fixed URI and its content | `resources/list` | `resources/read` | Configuration, documentation, or another stable document |
| `RegisterResourceTemplate` | A URI pattern and a callback | `resources/templates/list` | `resources/read` with a concrete URI | Files, records, or other data addressed by variables |
| `RegisterPrompt` | A reusable prompt and its arguments | `prompts/list` | `prompts/get` | Guided workflows and repeatable instructions |
| `RegisterTool` | A callable operation and typed input/output | `tools/list` | `tools/call` | Actions, calculations, and integrations |

Register all entries while constructing the server, before calling `Run` or
serving the first request:

~~~go
server, err := mcp.New(mcp.ServerInfo{Name: "catalog", Version: "1.0.0"})
if err != nil {
    return err
}
if err := server.RegisterResource(resource); err != nil {
    return err
}
if err := server.RegisterResourceTemplate(template); err != nil {
    return err
}
if err := server.RegisterPrompt(prompt); err != nil {
    return err
}
if err := mcp.RegisterTool(server, "lookup", "Look up a record", lookup); err != nil {
    return err
}
return server.Run(transport)
~~~

The first request seals this catalog. This is why registration belongs in
startup code and why a late registration returns `mcp.ErrStarted`. A typical
LLM interaction then follows these steps:

1. The MCP client sends `initialize` and receives the server capabilities.
2. The client asks for `tools/list`, `resources/list`,
   `resources/templates/list`, and `prompts/list` as supported by the server.
3. The host application converts the discovered metadata into the model's
   available context. The exact presentation is client-specific: a host can
   put tools into the model's tool definitions, offer prompts in a command
   picker, and read resources only when they are relevant.
4. The model chooses an action from that context. It emits a tool call, asks
   the host to read a resource, or selects a prompt; it never invokes a Go
   callback by itself.
5. The client sends the corresponding MCP request. go-mcp runs middleware,
   preserves the request context, validates/decodes parameters, and calls the
   registered handler when one is needed.
6. The client returns the response to the host. The host adds the result or
   prompt messages to the model conversation, and the model can continue or
   answer the user.

In other words, registration defines the server's MCP contract; the client is
the protocol boundary, and the LLM is one consumer of the metadata and
results. A model does not automatically receive every resource or execute
every prompt. The host/client controls when those operations are exposed.

#### `RegisterResource`: a stable piece of context

Use `RegisterResource` when the URI is known ahead of time and the content is
available as one value. A resource is data, not an operation:

~~~go
err := server.RegisterResource(mcp.Resource{
    URI:         "config://application",
    Name:        "Application configuration",
    Description: "Non-secret runtime settings",
    MIMEType:    "application/json",
    Text:        `{"environment":"production","region":"eu"}`,
})
~~~

The server advertises only the metadata (`uri`, `name`, `description`, and
`mimeType`) in `resources/list`. When the client needs the contents, it sends:

~~~json
{"jsonrpc":"2.0","id":7,"method":"resources/read",
 "params":{"uri":"config://application"}}
~~~

The response contains `contents` with the URI, MIME type, and text. A host may
then attach that text to the model context, quote it in a response, or decide
not to show it to the model. `RegisterResource` is a good fit for a schema,
README, policy, or a generated snapshot that should have one stable address.
Do not put credentials or other secrets in a resource unless the middleware
and the host's context policy explicitly protect them.

#### `RegisterResourceTemplate`: data addressed by variables

Use `RegisterResourceTemplate` when one logical resource has many concrete
URIs or must be computed at read time. The template is advertised as a
pattern; its callback runs only after a client requests a matching URI:

~~~go
err := server.RegisterResourceTemplate(mcp.ResourceTemplate{
    URITemplate: "record:///{id}",
    Name:        "Customer record",
    Description: "A customer record selected by id",
    MIMEType:    "application/json",
    Handler: func(ctx context.Context, request mcp.ResourceRequest) (mcp.Resource, error) {
        id := request.Variables["id"]
        record, err := loadRecord(ctx, id)
        if err != nil {
            return mcp.Resource{}, err
        }
        return mcp.Resource{
            URI:      request.URI,
            Name:     "Customer " + id,
            MIMEType: "application/json",
            Text:     record,
        }, nil
    },
})
~~~

The current matcher compares slash-separated segments. A placeholder such as
`{id}` matches one segment, and the extracted values are passed in
`ResourceRequest.Variables`; `ResourceRequest.URI` keeps the original URI.
The callback receives the request context, so cancellation can stop a slow
lookup. Validate identifiers and authorization inside the callback or
middleware before accessing storage; a URI template is routing metadata, not
an authorization rule.

The model normally does not invent a Go callback invocation. It sees the
template metadata through `resources/templates/list`; the host may let it
request a concrete URI such as `record:///42`, after which the client sends
`resources/read` and the server resolves the template.

#### `RegisterPrompt`: a reusable conversation starter

Use `RegisterPrompt` for a named, reusable instruction flow. Prompts are
selected by a user or host UI and returned as messages; they are not tools and
do not represent an imperative action.

A static prompt stores its messages in the catalog:

~~~go
err := server.RegisterPrompt(mcp.Prompt{
    Name:        "summarize",
    Description: "Ask for a concise summary",
    Arguments: []mcp.PromptArgument{
        {Name: "style", Description: "formal or casual", Required: true},
    },
    Messages: []mcp.PromptMessage{
        {Role: "user", Text: "Summarize the supplied document in a concise way."},
    },
})
~~~

A dynamic prompt uses `Handler` to render messages for each `prompts/get`
request:

~~~go
err := server.RegisterPrompt(mcp.Prompt{
    Name:        "review",
    Description: "Prepare a code-review conversation",
    Arguments: []mcp.PromptArgument{
        {Name: "language", Description: "Programming language", Required: true},
    },
    Handler: func(ctx context.Context, arguments map[string]string) ([]mcp.PromptMessage, error) {
        language := arguments["language"]
        if language == "" {
            return nil, errors.New("language is required")
        }
        return []mcp.PromptMessage{
            {Role: "user", Text: "Review this " + language + " code for correctness and security."},
        }, nil
    },
})
~~~

`prompts/list` exposes the name, description, and argument metadata. When a
client selects the prompt, it sends `prompts/get` with string arguments. The
server returns messages with roles and text; the host can insert them into the
conversation and let the model respond. `Required` documents the expected
input for clients; a dynamic handler should still validate arguments because
the host may send an incomplete map.

#### `RegisterTool`: an operation the model may call

Use `RegisterTool` for an operation with side effects, computation, or an
external integration. It is the only registration API in this group that
represents an action. The generic input and output types make the wire
contract explicit:

~~~go
type LookupInput struct {
    ID string `json:"id" mcp:"description=Customer identifier"`
}

func (value *LookupInput) UnmarshalJSON(data []byte) error {
    type alias LookupInput
    var decoded alias
    if err := json.Unmarshal(data, &decoded); err != nil {
        return err
    }
    *value = LookupInput(decoded)
    return nil
}

type LookupOutput struct {
    Name  string `json:"name"`
    Level string `json:"level"`
}

func (value *LookupOutput) MarshalJSON() ([]byte, error) {
    type alias LookupOutput
    return json.Marshal(alias(*value))
}

err := mcp.RegisterTool(server, "lookup", "Find a customer by identifier",
    func(ctx context.Context, input *LookupInput) (*LookupOutput, error) {
        return lookupCustomer(ctx, input.ID)
    })
~~~

At registration, go-mcp builds the input JSON Schema from the `json` and
`mcp` tags. In `tools/list` the client receives the tool name, description, and
`inputSchema`; the host can give that schema to the model. The model can then
emit a structured call such as:

~~~json
{"jsonrpc":"2.0","id":8,"method":"tools/call",
 "params":{"name":"lookup","arguments":{"id":"42"}}}
~~~

The server performs JSON decoding, runs the middleware chain, and invokes the
typed handler with the request context. A successful result is returned both
as `structuredContent` and as JSON text content, so clients that understand
structured output and clients that only consume text can both use it. An input
decoding failure or handler failure is represented as a safe tool error with
`isError: true`; implementation details are not sent to the model.

The model still decides whether to call the tool. A description that explains
when the operation is appropriate, precise field descriptions, and conservative
side-effect semantics help the model choose safely. Authorization belongs in
middleware or application code; a tool description is not a security control.

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
created and stops after the last session is removed. Transport shutdown closes
active sessions and stops cleanup immediately. Call `Close` when an SSE handler
is mounted in an application-owned HTTP server and is no longer needed.

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

`Content-Type: application/json` may include media-type parameters such as
`charset=utf-8`. JSON-RPC request IDs must be strings, numbers, or null; MCP
method parameters must be objects.

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
func (h *Handler) Close()
~~~

### Stdio limits and request observation

`stdio.NewTransport` keeps the 1 MiB default line limit. Use
`stdio.NewTransportWithConfig` or `stdio.ServeWithConfig` with
`stdio.Config{MaxMessageBytes: ...}` to choose another positive bound.

`WithRequestObserver` receives each successfully parsed request after its
handler returns, including the request duration. Observers run synchronously;
use them for lightweight metrics and tracing hooks.

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
calls Shutdown when a non-nil context is cancelled. A nil context leaves
shutdown under the caller's control.

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
| 2026-09-19 | Closing an SSE handler now terminates active sessions and cleanup immediately. |
| 2026-09-19 | Added strict request validation, URI-variable decoding, stdio limits, observers, fuzzing, and benchmarks. |
