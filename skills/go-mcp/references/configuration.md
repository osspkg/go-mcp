# Configuration reference

Read this reference when wiring environment or YAML configuration to server
transports.

## Choose one source

The two loaders are separate APIs. Choose exactly one for a process and do not
merge their returned structs:

```go
configuration, err := config.LoadEnv()
// or:
configuration, err := config.LoadYAML("server.yaml")
```

`LoadEnv` reads known variables with the `MCP_` prefix. `LoadYAML` accepts only
unindented `key: scalar` lines, optional comments, and quoted scalar values.
Nested mappings, lists, collections, unknown keys, malformed durations, and
non-positive limits are rejected.

## Supported keys

| Key suffix | Type | Meaning |
| --- | --- | --- |
| `NAME`, `VERSION`, `INSTRUCTIONS` | string | MCP server identity and instructions |
| `ENABLE_STDIO`, `ENABLE_HTTP`, `ENABLE_SSE` | bool | Enabled transports |
| `HTTP_ADDRESS` | string | HTTP listener address |
| `MCP_PATH`, `SSE_PATH`, `MESSAGE_PATH` | string | Transport routes |
| `READ_TIMEOUT`, `WRITE_TIMEOUT`, `IDLE_TIMEOUT` | duration | HTTP server timeouts, e.g. `15s` |
| `MAX_BODY_BYTES` | integer | Request body limit |
| `SESSION_TTL` | duration | Session expiry, e.g. `30m` |
| `MAX_SESSIONS` | integer | Maximum concurrent sessions |

Defaults are provided by `config.Default`: stdio is enabled, HTTP/SSE are
disabled, HTTP uses `:8080`, and the default body/session/timeouts are bounded.
The loaded configuration still needs a valid name/version and at least one
enabled transport.

## Build transports

`config.Transports` converts one loaded config into `mcp.Transport` values:

```go
transports, err := config.Transports(configuration, server, os.Stdin, os.Stdout)
if err != nil {
	return err
}
return server.Run(transports...)
```

When both HTTP and SSE are enabled, the returned HTTP transport serves `/mcp`,
`/sse`, and `/message` from one listener. When only one remote transport is
enabled, the corresponding adapter is returned. Stdio requires non-nil input
and output streams.

See [configuration examples](../examples/configuration.md) and the runnable
programs in `example/config-env` and `example/config-yaml`.
