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
```

## Examples

- `stdio` — newline-delimited JSON-RPC over stdin/stdout.
- `http` — Streamable HTTP on `POST /mcp`.
- `sse` — legacy SSE on `/sse` and `/message`.
- `middleware` — `Authorization` header validation.
- `config-env` — configuration from `MCP_*` environment variables.
- `config-yaml` — configuration from a flat YAML file.

All servers run through `server.Run(transports...)`. The library automatically
handles shutdown on `os.Interrupt` or `SIGTERM`.

For example, enable HTTP instead of stdio for the environment configuration
example:

```bash
MCP_ENABLE_STDIO=false MCP_ENABLE_HTTP=true go run ./example/config-env
```
