---
name: go-mcp
description: Build, extend, test, and document stdlib-only MCP servers with go.osspkg.com/mcp across stdio, Streamable HTTP, and legacy SSE.
metadata:
  short-description: Work with the go-mcp server library
---

# go-mcp

Use this skill when a task builds or maintains an MCP server with this
repository's `go.osspkg.com/mcp` module. Do not apply it to generic Go work or
to a different MCP implementation unless the user explicitly asks for a
porting comparison.

## Non-negotiable constraints

- Runtime code and `go.mod` remain standard-library-only. Do not add YAML,
  HTTP, JSON-RPC, logging, or signal dependencies; the repository implements
  those boundaries with the standard library.
- Target MCP revision is `2025-11-25`. Streamable HTTP is the primary remote
  transport; SSE is legacy compatibility; stdio is newline-delimited JSON-RPC.
- Keep the core package transport-independent. Transport packages may import
  `mcp`, but `mcp` must not import `mcp/stdio`, `mcp/http`, or `mcp/sse`.
- Register tools, resources, templates, and prompts before the first request.
  The first `ServeJSON` call seals the registry; duplicate names and late
  registration must return errors rather than mutate live state.
- Preserve request contexts through middleware, typed tool handlers, dynamic
  resources, prompts, and transport shutdown. Never write diagnostics to the
  stdio protocol writer.
- Public identifiers need accurate godoc. Keep examples buildable with
  `go test ./...` and avoid examples that require network services or secrets.

## Runtime and API decisions

- `server.Run(transports...)` owns the normal process lifecycle. It creates a
  signal context for `os.Interrupt` and `syscall.SIGTERM`, starts transports
  concurrently, cancels siblings after a transport error, waits for all
  transports, and returns `context.Canceled` for signal-triggered shutdown.
- `server.RunContext(ctx, transports...)` is the escape hatch for an embedding
  application or a test that owns cancellation. Do not reintroduce signal
  setup in every example when `Run` is appropriate.
- Build transports with `stdio.NewTransport`, `http.NewTransport`,
  `http.NewTransportWithSSE`, or `sse.NewTransport`. Use `config.Transports`
  when configuration selects transports; HTTP and SSE share one listener when
  both are enabled through that adapter.
- Use `go.osspkg.com/mcp/client` when an application must consume a server:
  start `client.NewHTTPTransport`, `client.NewStdioTransport`, or
  `client.NewSSETransport`, complete `Initialize`, then use the typed catalog
  and task helpers. Register `OnRequest` handlers for server-initiated roots,
  sampling, or elicitation before invoking tools.
- Typed tools use `RegisterTool[In, Out]` inference. `In` and `Out` are
  pointers to structs implementing `json.Unmarshaler` and `json.Marshaler`.
  `json` tags define wire names and required fields (`omitempty` makes a field
  optional); `mcp:"description=..."` populates schema descriptions.
- Middleware wraps every MCP method, including `initialize` and listings.
  HTTP/SSE headers are available in `request.Meta.Headers`; map authorization
  failures to `mcp.ErrUnauthorized` or `mcp.ErrForbidden` instead of exposing
  implementation errors.
- Configuration callers choose exactly one loader: `config.LoadEnv()` for
  `MCP_*` variables or `config.LoadYAML(path)` for the supported flat scalar
  YAML subset. Do not merge the returned values.

## Task workflow

1. Inspect `AGENTS.md`, `DOC.md`, the current package API, and the relevant
   runnable example before changing behavior. Preserve unrelated worktree
   changes.
2. Select the smallest transport-independent core change and keep transport
   details in the appropriate subpackage. Route to the references below before
   making lifecycle, config, or protocol decisions.
3. Add or update focused tests for malformed JSON-RPC, cancellation,
   authorization, limits, registry sealing, and concurrency whenever the
   change touches those paths.
4. Update the relevant runnable example and godoc. Keep protocol output and
   diagnostics on their documented streams.
5. Run `make lint` and `make tests`. For transport or concurrency changes also
   run `go test -race ./...` and `git diff --check`. `make lint` can report a
   non-fatal `govulncheck` network warning when the vulnerability database is
   unreachable; report that limitation truthfully.

## Supporting material

Read only the references relevant to the current task:

- [API reference](references/api_reference.md) for package boundaries,
  registration, typed tools, middleware, and protocol behavior.
- [Lifecycle reference](references/lifecycle.md) for `Run`, `RunContext`,
  signal handling, and transport shutdown.
- [Configuration reference](references/configuration.md) for env/YAML keys,
  defaults, validation, and transport assembly.
- [Testing reference](references/testing.md) for project quality gates and
  focused test expectations.
- [Transport examples](examples/transports.md),
- [Client examples](examples/client.md),
  [middleware example](examples/middleware.md),
  [catalog example](examples/catalog.md), and
  [configuration examples](examples/configuration.md) when writing or
  reviewing sample servers.

The repository's complete runnable examples live under `example/`; use those
files as the source of truth when a snippet in a reference needs to be
expanded.
