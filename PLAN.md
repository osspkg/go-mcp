# go-mcp implementation plan

## Goal and constraints

Create the stdlib-only module `go.osspkg.com/mcp` for MCP `2025-11-25`.
Runtime dependencies are forbidden. The public packages are the root `mcp` API
and `config`, `stdio`, `http`, and `sse` transports. Streamable HTTP is the
primary remote transport; SSE exists for legacy interoperability. Every task
must pass `make lint` and `make tests`; transport/concurrency tasks also run
`go test -race ./...`.

## Tasks

1. **Project conventions and documentation.** Maintain this plan and
   `AGENTS.md`; document the stdlib boundary, protocol revision, stdout rule
   for stdio, mandatory quality gates, and exported API with godoc.
2. **Core server and JSON-RPC.** Implement `Server`, server identity,
   constructor options, registry sealing, JSON-RPC 2.0 parsing, safe errors,
   and MCP `initialize`/`ping` dispatch. Invalid input must never panic or
   disclose implementation errors.
3. **Typed tools and JSON Schema.** Implement generic `RegisterTool[In, Out]`.
   Models are pointer structs implementing `json.Unmarshaler` and
   `json.Marshaler`; `json` tags choose names and optionality, while
   `mcp:"description=…"` documents fields. Reject unsupported shapes during
   registration, not on a live request.
4. **Catalog API.** Add deterministic listings and safe handlers for static
   resources, URI templates, static prompts, and dynamic prompts. Cover
   `tools/list`, `tools/call`, `resources/list`, `resources/templates/list`,
   `resources/read`, `prompts/list`, and `prompts/get`.
5. **Authorization pipeline.** Run middleware around every MCP method. Make
   request method, transport name, session ID, and HTTP headers available.
   Map `ErrUnauthorized` and `ErrForbidden` to safe JSON-RPC errors and to HTTP
   401/403 when a transport can express those status codes.
6. **Configuration.** Provide mutually exclusive `config.LoadEnv()` and
   `config.LoadYAML(path)`. Support only flat YAML `key: scalar` values; reject
   nesting, lists, unknown keys, invalid durations, and unsafe limits. Define
   `MCP_*` names for identity, transports, routes, limits, timeouts, and
   session bounds. Convert a loaded configuration to transport adapters with a
   single listener for combined HTTP and SSE.
7. **Stdio transport.** Serve newline-delimited JSON-RPC, enforce a bounded
   scanner, propagate context cancellation, serialize writes, and never emit
   diagnostics to the protocol output stream.
8. **Streamable HTTP transport.** Build a `net/http` handler for `POST /mcp`.
   Enforce JSON content type and body bounds; generate cryptographically random
   bounded, expiring sessions after initialization; pass request headers to
   middleware; return correct content type and authentication status.
9. **Legacy SSE transport.** Expose `GET /sse` and its advertised
   `POST /message?sessionId=…` endpoint. Keep session/message queues bounded,
   handle client disconnects and cancellation, validate requests before queue
   writes, and release sessions after expiry or disconnect.
10. **Concurrent runtime composition.** Keep the core independent of transport
    imports using the `Transport` interface. `Server.Run(transports...)`
    starts configured transports concurrently, owns os.Interrupt/SIGTERM cancellation
    and shutdown, and waits for all transports. `Server.RunContext(ctx, ...)`
    remains available when the embedding application owns the lifecycle. HTTP
    and legacy SSE can be mounted on one listener with
    `http.NewTransportWithSSE`.
11. **Examples and regression coverage.** Add README examples for typed tools,
    middleware, env/YAML config, stdio, Streamable HTTP, and SSE. Test malformed
    JSON-RPC, catalog operations, schema generation, duplicate/late
    registration, auth, config validation, HTTP sessions and limits, SSE
    lifecycle, shutdown, and races.

## Acceptance criteria

- Library source imports only the Go standard library.
- All public symbols have accurate documentation and examples compile.
- `make lint`, `make tests`, `go test ./...`, `go test -race ./...`, and
  `git diff --check` pass.
- No unbounded body read, session map, message queue, goroutine, or stdout log
  path is introduced.
