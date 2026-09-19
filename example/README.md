# Примеры go-mcp

Каждый каталог содержит самостоятельный `main.go` и запускается без внешних
зависимостей:

```bash
go run ./example/stdio
go run ./example/http
go run ./example/sse
go run ./example/middleware
go run ./example/config-env
go run ./example/config-yaml -- example/config-yaml/server.yaml
```

## Примеры

- `stdio` — newline-delimited JSON-RPC через stdin/stdout.
- `http` — Streamable HTTP на `POST /mcp`.
- `sse` — legacy SSE на `/sse` и `/message`.
- `middleware` — проверка заголовка `Authorization`.
- `config-env` — конфигурация из переменных `MCP_*`.
- `config-yaml` — конфигурация из плоского YAML-файла.

Все серверы запускаются через `server.Run(transports...)`. Остановка по
`os.Interrupt` или `SIGTERM` выполняется библиотекой автоматически.

Для env-примера, например, можно включить HTTP вместо stdio:

```bash
MCP_ENABLE_STDIO=false MCP_ENABLE_HTTP=true go run ./example/config-env
```

