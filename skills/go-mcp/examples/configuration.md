# Configuration examples

Choose one source per process and then pass the resulting config through
`config.Transports`.

## Environment

```bash
MCP_NAME=env-example \
MCP_VERSION=1.0.0 \
MCP_ENABLE_STDIO=false \
MCP_ENABLE_HTTP=true \
MCP_HTTP_ADDRESS=:8080 \
go run ./example/config-env
```

The Go side is:

```go
configuration, err := config.LoadEnv()
transports, err := config.Transports(configuration, server, os.Stdin, os.Stdout)
return server.Run(transports...)
```

## YAML

```yaml
name: yaml-example
version: 1.0.0
enable_stdio: false
enable_http: true
http_address: ":8080"
mcp_path: /mcp
```

```go
configuration, err := config.LoadYAML("server.yaml")
transports, err := config.Transports(configuration, server, os.Stdin, os.Stdout)
return server.Run(transports...)
```

The YAML reader is deliberately not a general YAML parser: nested values,
lists, aliases, collections, and unknown keys are errors. Complete runnable
versions are in `example/config-env` and `example/config-yaml`.
