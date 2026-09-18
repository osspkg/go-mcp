# Project instructions

`go-mcp` is a stdlib-only Go library for building MCP servers. Runtime packages
must not add module dependencies. Keep the public API small, document every
exported identifier, and preserve context cancellation through handlers and
transports.

Run `make lint` and `make tests` before completing each implementation task.
Also run `go test -race ./...` for concurrency-sensitive changes. Do not write
protocol data to stdout from the stdio transport except JSON-RPC messages.

The supported MCP revision is `2025-11-25`. Streamable HTTP is the primary
remote transport; SSE is a legacy compatibility transport.
