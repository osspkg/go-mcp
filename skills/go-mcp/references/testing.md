# Testing and quality gates

Read this reference before declaring a go-mcp change complete.

## Required commands

Run from the repository root:

```bash
make lint
make tests
go test -race ./...
git diff --check
```

`make tests` runs the unit subset with the race detector and coverage. The
standalone race command covers all packages, including `example/*`, so new
examples must compile without external services.

`make lint` runs goppy, goimports, gofmt, golangci-lint, and govulncheck. A
read-only golangci cache only produces warnings. If govulncheck cannot reach
`vuln.go.dev`, record that the vulnerability database was unavailable; do not
claim a clean vulnerability scan.

## Focused coverage

- Core: malformed JSON-RPC, unknown methods, safe errors, registry sealing,
  duplicate names, typed schema and codec behavior.
- Middleware: method/metadata propagation, unauthorized/forbidden mappings,
  and HTTP/SSE status behavior.
- Transports: handshake, content types, body limits, session expiry, client
  disconnects, EOF, cancellation, graceful shutdown, and malformed requests.
- Runtime: all transports start concurrently; cancellation and transport
  failure stop siblings; `Run` waits before returning. Use deterministic
  channels in tests rather than sleeps.

Never assert protocol responses by reading logs from stdout in a stdio test;
stdout is protocol data and diagnostics belong on stderr.
