# go-mcp project instructions

## Project status and purpose

go-mcp is a stdlib-only Go library for building MCP servers compatible with MCP
revision 2025-11-25. The public module is `go.osspkg.com/mcp` and the runtime
packages must not add third-party dependencies.

The supported transports are:

- newline-delimited JSON-RPC over stdio;
- Streamable HTTP as the primary remote transport;
- legacy HTTP+SSE for compatibility.

The core package also provides typed tools, resources, resource templates,
prompts, JSON-RPC dispatch, middleware, authorization errors, and lifecycle
management.

## Source of truth

Use this priority when information conflicts:

1. current source code and tests;
2. this file for repository workflow and invariants;
3. the public API documentation;
4. examples and skill references.

Do not recreate or reintroduce `PLAN.md`. The active planning history has been
retired; use the current API, tests, documentation, and issue/task request as
the implementation context.

The documentation set is intentionally split by audience:

| File | Role |
| --- | --- |
| `README.md` | GitHub landing page and minimal English onboarding |
| `DOC.md` | Complete English API and developer guide |
| `DOC.ru.md` | Complete Russian API and developer guide |
| `example/README.md` | English index of runnable examples |
| `skills/go-mcp/SKILL.md` | AI-assisted development workflow and references |
| `LICENSE` | BSD 3-Clause legal terms |

## Non-negotiable runtime rules

- Keep runtime code and `go.mod` standard-library-only.
- Keep the `mcp` core independent from `mcp/stdio`, `mcp/http`, and `mcp/sse`.
- Target MCP revision 2025-11-25 unless a task explicitly changes the
  compatibility target.
- Preserve request contexts through middleware, typed handlers, dynamic
  resources, prompts, and transport shutdown.
- Do not write diagnostics or logs to the stdio protocol writer. stdout is
  reserved for JSON-RPC messages; diagnostics belong on stderr.
- Keep the public API small and document every exported identifier with
  accurate Go documentation.
- Validate user-controlled body sizes, session identifiers, URI-template
  variables, timeouts, and configuration values.
- Do not add CORS, authentication providers, TLS termination, or business
  storage implicitly. Keep those policies in application code or a proxy.

## API and behavior invariants

- Register tools, resources, templates, and prompts before the first request.
  The first `ServeJSON` call seals the registry.
- Reject duplicate tool names, resource URIs, template URIs, and prompt names.
  Late registration must return `ErrStarted`.
- Typed tools use `RegisterTool[In, Out]`. Input and output types are pointers
  to application structures implementing `json.Unmarshaler` and
  `json.Marshaler`.
- `json` tags define wire names and required fields; `omitempty` makes a field
  optional; `mcp:"description=..."` supplies schema descriptions.
- Apply one middleware pipeline to every MCP method, including `initialize` and
  catalog listings. Map `ErrUnauthorized` and `ErrForbidden` to safe protocol
  errors and HTTP 401/403 responses.
- `Server.Run(transports...)` owns normal process signals
  (`os.Interrupt` and `SIGTERM`), cancels all transports, waits for them, and
  returns `context.Canceled` on signal-triggered shutdown.
- Use `Server.RunContext(ctx, transports...)` when the embedding application
  owns cancellation. Do not duplicate signal setup in every example.
- Stdio remains newline-delimited JSON-RPC. Streamable HTTP and SSE must enforce
  content types, body limits, session limits, and configured timeouts.
- SSE sessions use a bounded 16-message queue. Expired sessions are removed by
  the background cleanup goroutine, which also signals their SSE stream to
  terminate. Preserve backpressure and request-context cancellation.
- `config.LoadEnv` and `config.LoadYAML` are separate sources. Do not merge
  their results. YAML remains a flat `key: scalar` subset.

## Repository layout

| Path | Responsibility |
| --- | --- |
| root Go files | core server, registry, JSON-RPC, typed tools, lifecycle |
| `stdio/` | stdio framing and cancellation |
| `http/` | Streamable HTTP and combined HTTP/SSE listener |
| `sse/` | legacy SSE sessions, queue, and cleanup |
| `config/` | env/YAML parsing and transport assembly |
| `example/` | buildable, dependency-free sample servers |
| `skills/go-mcp/` | reusable agent skill, references, and examples |
| `README.md` / `DOC*.md` | user documentation |

Keep transport-specific behavior out of the core. Prefer small focused files
and table-driven tests. Preserve unrelated worktree changes.

## Development workflow

For every behavior or API change:

1. Read this file, the relevant package source, tests, and documentation before
   editing.
2. Check the public API with `go doc` and inspect existing examples.
3. Make the smallest change that preserves the package boundaries and context
   behavior.
4. Add regression tests for malformed input, cancellation, authorization,
   limits, duplicate registration, session cleanup, or races when applicable.
5. Update Go doc comments, README examples, DOC.md, DOC.ru.md, and
   example/README.md when the public behavior changes.
6. Run the quality gates below and inspect the final diff.

Do not commit generated coverage artifacts or local tool output unless the task
explicitly requests them. Do not commit secrets, tokens, local configuration,
or dependency caches.

## Required quality gates

Run these before completing implementation or transport/concurrency work:

~~~text
make lint
make tests
go test -race ./...
git diff --check
~~~

For documentation-only changes, at minimum run `git diff --check` and validate
links, language expectations, and code fences. Running the full Go gates is
still encouraged when the documentation changes examples or API claims.

The Makefile delegates formatting, vet, static analysis, and tests to goppy.
`make lint` may run `go mod tidy`, `goimports`, and `gofmt`; inspect the worktree
afterward. A `govulncheck` warning caused by an unreachable vulnerability
database is an environment limitation, not a reason to hide or ignore the
warning in the final report.

For concurrency-sensitive changes, do not stop at package tests: run the full
race suite and review goroutine, channel, session, and shutdown ownership.

## Documentation maintenance

Keep documentation synchronized with the implementation rather than treating it
as a one-time deliverable.

When an exported API or protocol behavior changes:

- update the Go doc comment first;
- update the matching sections in `DOC.md` and `DOC.ru.md`;
- update the short usage path in `README.md`;
- update or add a runnable example under `example/`;
- update `skills/go-mcp/references/` and `skills/go-mcp/examples/` when the
  workflow or contract affects AI-assisted development;
- add or adjust focused tests;
- update each documentation changelog if the change is user-visible.

Maintain English-only content in `README.md`, `DOC.md`, and
`example/README.md`. Keep the complete Russian translation in `DOC.ru.md`.
Examples in both API guides must compile conceptually against the current
exported signatures; do not document removed functions or stale defaults.

Review freshness:

- before every release or MCP revision update;
- whenever a public type, method, default, endpoint, error code, or shutdown
  behavior changes;
- at least once per quarter for links, examples, badges, toolchain versions,
  dependency policy, and license text.

The freshness review should compare the docs with `go doc ./...`, the current
tests, `go.mod`, `Makefile`, `LICENSE`, and all runnable examples. Check that
all links resolve, code fences are balanced, and the README badges still point
to valid project endpoints.

## Change review checklist

Before handing work back, confirm:

- no runtime dependency was added;
- public API and MCP revision remain explicit;
- contexts and cancellations are preserved;
- stdio stdout contains protocol data only;
- malformed input and authorization behavior remain safe;
- session cleanup and bounded queues cannot leak or panic;
- duplicate and late registration are covered;
- docs and examples match the code;
- `make lint`, `make tests`, and race tests have truthful results;
- the final worktree contains only intended changes.

When a required check cannot run because of the environment, report the exact
command, the limitation, and which alternative checks passed.
