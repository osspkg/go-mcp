# go-mcp examples

Each directory contains a standalone `main.go` and runs without external
dependencies:

```bash
go run ./example/stdio
go run ./example/http
go run ./example/sse
go run ./example/middleware
go run ./example/config-env
go run ./example/config-yaml -- example/config-yaml/server.yaml
go run ./example/features
go run ./example/tasks
go run ./example/discovery
```

## Examples

- `stdio` — newline-delimited JSON-RPC over stdin/stdout.
- `http` — Streamable HTTP on `POST /mcp` and resumable `GET /mcp` events.
- `sse` — legacy SSE on `/sse` and `/message`.
- `middleware` — `Authorization` header validation.
- `config-env` — configuration from `MCP_*` environment variables.
- `config-yaml` — configuration from a flat YAML file.
- `features` — capabilities, tool annotations, icons, output schema, progress, logging, roots, sampling, and elicitation.
- `tasks` — task-augmented tool execution with cancellation and polling.
- `discovery` — OAuth protected-resource and authorization-server metadata.

All MCP servers run through `server.Run(transports...)`. The library
automatically handles shutdown on `os.Interrupt` or `SIGTERM`. The standalone
`discovery` example serves only OAuth/OIDC metadata with `net/http`.

The HTTP transport also carries server notifications and correlated
server-initiated requests (sampling, roots, and elicitation) over the session
stream. See [DOC.md](../DOC.md) for Tasks, OAuth/OIDC discovery metadata, and
tool annotations.

For example, enable HTTP instead of stdio for the environment configuration
example:

```bash
MCP_ENABLE_STDIO=false MCP_ENABLE_HTTP=true go run ./example/config-env
```
