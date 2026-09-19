# go-mcp: API и руководство разработчика

Статус документа: актуально для ревизии MCP 2025-11-25.

Модуль: go.osspkg.com/mcp

Библиотека реализует stdlib-only сервер Model Context Protocol с единым ядром и
тремя транспортами: newline-delimited JSON через stdio, Streamable HTTP и
legacy SSE. Вы регистрируете инструменты, resources и prompts, подключаете
middleware и запускаете включённые транспорты через Run.

## Содержание

1. [Назначение и границы](#1-назначение-и-границы)
2. [Архитектура](#2-архитектура)
3. [Быстрый старт](#3-быстрый-старт)
4. [Основной API пакета mcp](#4-основной-api-пакета-mcp)
5. [Typed tools и JSON Schema](#5-typed-tools-и-json-schema)
6. [Resources и prompts](#6-resources-и-prompts)
7. [Middleware и авторизация](#7-middleware-и-авторизация)
8. [JSON-RPC и методы MCP](#8-json-rpc-и-методы-mcp)
9. [Транспорты](#9-транспорты)
10. [Конфигурация](#10-конфигурация)
11. [Жизненный цикл и остановка](#11-жизненный-цикл-и-остановка)
12. [Сценарии использования](#12-сценарии-использования)
13. [Ошибки и безопасность](#13-ошибки-и-безопасность)
14. [Тестирование и качество](#14-тестирование-и-качество)
15. [Связанные материалы и изменения](#15-связанные-материалы-и-изменения)

## 1. Назначение и границы

go-mcp предназначена для разработчиков, которым нужно встроить MCP-сервер в
CLI, локальный агент или HTTP-сервис без внешних runtime-зависимостей. go.mod
содержит только стандартную библиотеку Go. Линтеры и инструменты разработки не
становятся зависимостями приложения.

Поддерживаются:

- MCP 2025-11-25;
- JSON-RPC 2.0;
- tools, resources, resource templates и prompts;
- stdio, Streamable HTTP и legacy SSE;
- единый middleware pipeline для всех MCP-вызовов;
- конфигурация из MCP_* или плоского YAML.

Библиотека не включает хранилище пользователей, JWT/OAuth-провайдер,
CORS-политику, TLS-терминацию или бизнес-логику. Эти части остаются в
приложении или внешнем reverse proxy.

## 2. Архитектура

Публичные пакеты разделены по ответственности:

| Пакет | Назначение |
| --- | --- |
| mcp | сервер, реестр, JSON-RPC, typed tools, middleware, каталог MCP |
| mcp/config | загрузка и валидация конфигурации |
| mcp/stdio | newline-delimited JSON transport |
| mcp/http | Streamable HTTP и объединение с legacy SSE |
| mcp/sse | legacy SSE transport |

Ядро mcp не импортирует транспортные пакеты. Транспорт получает указатель на
сервер через интерфейс:

~~~go
type Transport interface {
    Serve(context.Context, *mcp.Server) error
}
~~~

Поток запроса выглядит так:

1. транспорт принимает байты и формирует mcp.Request;
2. сервер запускает middleware с контекстом запроса;
3. JSON-RPC dispatch выбирает MCP-метод;
4. реестр вызывает tool/resource/prompt handler;
5. транспорт сериализует ответ и управляет соединением или сессией.

## 3. Быстрый старт

Ниже приведён минимальный typed tool сервер на stdio. Модели инструмента
являются указателями на пользовательские структуры и реализуют
json.Unmarshaler/json.Marshaler (обычно это делает encoding/json через явные
методы).

~~~go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"

    "go.osspkg.com/mcp"
    "go.osspkg.com/mcp/stdio"
)

type AddInput struct {
    A int `json:"a" mcp:"description=Первое слагаемое"`
    B int `json:"b" mcp:"description=Второе слагаемое"`
}

func (v *AddInput) UnmarshalJSON(data []byte) error {
    type alias AddInput
    var value alias
    if err := json.Unmarshal(data, &value); err != nil {
        return err
    }
    *v = AddInput(value)
    return nil
}

type AddOutput struct {
    Result int `json:"result"`
}

func (v *AddOutput) MarshalJSON() ([]byte, error) {
    type alias AddOutput
    return json.Marshal(alias(*v))
}

func main() {
    server, err := mcp.New(mcp.ServerInfo{Name: "calculator", Version: "1.0.0"})
    if err != nil {
        panic(err)
    }
    if err := mcp.RegisterTool(server, "add", "Складывает два числа",
        func(_ context.Context, input *AddInput) (*AddOutput, error) {
            return &AddOutput{Result: input.A + input.B}, nil
        }); err != nil {
        panic(err)
    }

    transport := stdio.NewTransport(os.Stdin, os.Stdout)
    if err := server.Run(transport); err != nil {
        panic(fmt.Errorf("mcp server: %w", err))
    }
}
~~~

Run сам создаёт контекст, который отменяется по os.Interrupt или syscall.SIGTERM.
Если жизненный цикл принадлежит вызывающему коду, используйте RunContext.

## 4. Основной API пакета mcp

### Создание сервера

~~~go
type ServerInfo struct {
    Name         string
    Version      string
    Instructions string
}

type Option func(*Server) error

func New(info ServerInfo, options ...Option) (*Server, error)
func WithMiddleware(middleware ...Middleware) Option
~~~

Name и Version обязательны. Instructions передаются клиенту в ответе
initialize. WithMiddleware сохраняет порядок аргументов: первый middleware в
списке получает запрос первым.

New возвращает ошибку для пустых обязательных полей, nil option или nil
middleware. Сервер не готовит транспорт сам: транспорт передаётся в Run.

### Запрос и обработчик

~~~go
type RequestMeta struct {
    Transport string
    Headers   map[string]string
    SessionID string
}

type Request struct {
    Method string
    Params json.RawMessage
    Meta   RequestMeta
}

type Handler func(context.Context, Request) (any, error)
type Middleware func(Handler) Handler
~~~

RequestMeta.Transport имеет значение stdio, http или sse. HTTP и SSE передают
копию входящих заголовков и идентификатор MCP-сессии. Middleware может получить
метод из Request.Method, а отмену — из context.Context.

### Прямой JSON-RPC вызов

~~~go
func (s *Server) ServeJSON(
    ctx context.Context,
    payload []byte,
    meta RequestMeta,
) ([]byte, error)
~~~

Метод удобен для собственного транспорта и тестов. Он разбирает один JSON-RPC
запрос, применяет тот же registry и middleware pipeline, что и штатные
транспорты. Некорректный JSON получает parse error JSON-RPC; неизвестный метод,
неверные параметры и ошибки handler кодируются безопасными ошибками протокола.

### Каталог и фиксация реестра

Методы регистрации:

~~~go
func (s *Server) RegisterResource(resource Resource) error
func (s *Server) RegisterResourceTemplate(template ResourceTemplate) error
func (s *Server) RegisterPrompt(prompt Prompt) error
~~~

Имена tools/prompts и URI ресурсов должны быть уникальными в своей категории.
При первом запросе реестр фиксируется. После этого регистрация возвращает
ErrStarted; это позволяет безопасно обслуживать запросы конкурентно и
гарантирует стабильные ответы */list.

## 5. Typed tools и JSON Schema

Основной API инструмента — generic-функция:

~~~go
type ToolHandler[In json.Unmarshaler, Out json.Marshaler] func(
    context.Context,
    In,
) (Out, error)

func RegisterTool[In json.Unmarshaler, Out json.Marshaler](
    server *Server,
    name string,
    description string,
    handler ToolHandler[In, Out],
) error
~~~

In и Out должны быть указателями на структуры приложения. Обязательное условие
— методы UnmarshalJSON у входа и MarshalJSON у результата. Это делает
контракт явным и позволяет валидировать JSON-кодек при регистрации.

JSON Schema строится из полей и тегов:

| Запись | Результат |
| --- | --- |
| `json:"user_id"` | имя свойства user_id |
| `json:"name,omitempty"` | необязательное свойство name |
| `json:"name"` | обязательное свойство name |
| `json:"-"` | поле исключается |
| `mcp:"description=..."` | описание свойства |

Поддерживаются scalar-типы, указатели, вложенные структуры, slices и arrays.
Неподдерживаемый тип или некорректный тег отклоняется до запуска сервера.

Успешный результат tools/call содержит сериализованный output в
structuredContent и JSON text content. Ошибка декодирования входа или
handler-ошибка возвращается как результат инструмента с isError: true; в
текст не попадают внутренние детали реализации.

## 6. Resources и prompts

### Статический и динамический resource

~~~go
type Resource struct {
    URI         string
    Name        string
    Description string
    MIMEType    string
    Text        string
}

type ResourceRequest struct {
    URI       string
    Variables map[string]string
}

type ResourceHandler func(context.Context, ResourceRequest) (Resource, error)
~~~

Статический resource регистрируется заполненным Text. Для вычисляемого
содержимого задайте ResourceTemplate:

~~~go
type ResourceTemplate struct {
    URITemplate string
    Name        string
    Description string
    MIMEType    string
    Handler     ResourceHandler
}
~~~

Шаблоны используют slash-сегменты и именованные placeholders вида
file:///{path}; один placeholder соответствует одному сегменту URI. При
resources/read сервер передаёт разобранные значения в Variables. URI и имя
обязательны, template обязан иметь handler.

### Prompts

~~~go
type PromptMessage struct {
    Role string
    Text string
}

type PromptArgument struct {
    Name        string
    Description string
    Required    bool
}

type PromptHandler func(context.Context, map[string]string) ([]PromptMessage, error)

type Prompt struct {
    Name        string
    Description string
    Arguments   []PromptArgument
    Messages    []PromptMessage
    Handler     PromptHandler
}
~~~

Для статического prompt задайте Messages. Для динамического задайте Handler;
callback получает аргументы prompts/get и может вернуть разные сообщения для
каждого запроса. Нельзя создать prompt без messages и handler.

## 7. Middleware и авторизация

Middleware применяется к initialize, discovery-методам и вызовам каталога
одинаково на всех транспортах. Пример bearer-проверки:

~~~go
func requireToken(next mcp.Handler) mcp.Handler {
    return func(ctx context.Context, req mcp.Request) (any, error) {
        if req.Meta.Headers["Authorization"] != "Bearer secret" {
            return nil, mcp.ErrUnauthorized
        }
        return next(ctx, req)
    }
}

server, err := mcp.New(info, mcp.WithMiddleware(requireToken))
~~~

Проверяйте также req.Meta.Transport, req.Meta.SessionID и собственные
заголовки. Middleware обязан уважать отмену контекста и не должен писать ответ
самостоятельно.

| Ошибка handler | JSON-RPC code | HTTP status |
| --- | ---: | ---: |
| mcp.ErrUnauthorized | -32001 | 401 |
| mcp.ErrForbidden | -32003 | 403 |
| прочая ошибка | -32603 или безопасная tool error | 200/500 по контексту |

Для stdio эти ошибки остаются JSON-RPC ошибками; HTTP/SSE добавляют
соответствующий статус, не раскрывая секреты.

## 8. JSON-RPC и методы MCP

Поддерживаемые методы:

| Метод | Назначение |
| --- | --- |
| initialize | handshake, protocol version, capabilities и server info |
| ping | проверка доступности |
| tools/list | список зарегистрированных tools и input schema |
| tools/call | запуск typed tool |
| resources/list | статические resources |
| resources/templates/list | URI templates |
| resources/read | чтение статического или динамического resource |
| prompts/list | список prompts и аргументов |
| prompts/get | получение сообщений prompt |

Ответ initialize объявляет protocol version 2025-11-25 и capabilities
tools/resources/prompts. Неизвестный метод получает -32601, неверные параметры
— -32602, некорректный JSON — -32700. JSON-RPC notification без id не требует
ответа.

## 9. Транспорты

### Stdio

Пакет go.osspkg.com/mcp/stdio:

~~~go
transport := stdio.NewTransport(os.Stdin, os.Stdout)
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

Если transport не нужно сохранять как значение, тот же цикл можно запустить
функцией:

~~~go
err := stdio.Serve(ctx, server, os.Stdin, os.Stdout)
~~~

Каждая строка stdin — один JSON-RPC объект, каждая строка stdout — ответ.
Лимит строки — 1 MiB. Протокольные данные идут только в stdout; диагностику
пишите в stderr. При отмене контекста transport закрывает вход, если он
реализует io.Closer.

### Streamable HTTP

Пакет go.osspkg.com/mcp/http (обычно импортируется как mcphttp):

~~~go
transport := mcphttp.NewTransport(mcphttp.Config{
    Address: ":8080",
    Path:    "/mcp",
})
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

POST на /mcp принимает application/json. На initialize создаётся
Mcp-Session-Id; последующие запросы должны передавать тот же заголовок.
Поддерживаются ограничения тела, TTL и максимальное число сессий, read/write/
idle timeout и graceful shutdown. CORS не добавляется автоматически.

Для встраивания в существующий mux используйте:

~~~go
handler, err := mcphttp.NewHandler(server, mcphttp.DefaultConfig())
if err != nil {
    log.Fatal(err)
}
http.Handle("/mcp", handler)
~~~

Для запуска обычного net/http.Server доступен helper mcphttp.Server:

~~~go
srv := mcphttp.Server(ctx, mcphttp.ServerConfig{
    Address: ":8080",
    Handler: handler,
})
err := srv.ListenAndServe()
~~~

### Legacy SSE

Пакет go.osspkg.com/mcp/sse сохраняет совместимость с клиентами, которым
нужны GET /sse и POST /message:

~~~go
transport := sse.NewTransport(sse.Config{
    SSEPath:     "/sse",
    MessagePath: "/message",
})
if err := server.Run(transport); err != nil {
    log.Fatal(err)
}
~~~

GET /sse возвращает text/event-stream и событие endpoint с URL message
endpoint. POST /message?sessionId=... помещает ответ в SSE-очередь и использует
text/event-stream для доставки. На каждую сессию создаётся буферизованный канал
на 16 сообщений. Если канал заполнен, POST ждёт освобождения места или отмены
своего context. Фоновый cleanup запускается при появлении первой сессии,
проверяет TTL и закрывает истёкшие SSE-потоки; когда сессий не осталось,
goroutine завершается. Сессия также удаляется при разрыве клиента.

HTTP transport можно создать с legacy SSE на одном listener:

~~~go
transport := mcphttp.NewTransportWithSSE(
    mcphttp.Config{Address: ":8080", Path: "/mcp"},
    sse.Config{SSEPath: "/sse", MessagePath: "/message"},
)
~~~

Полные настройки HTTP transport:

~~~go
type Config struct {
    Address      string
    Path         string
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func DefaultConfig() Config
func NewTransport(config Config) *Transport
func NewTransportWithSSE(config Config, legacy sse.Config) *Transport
func NewHandler(server *mcp.Server, config Config) (*Handler, error)
~~~

Для SSE поля Config совпадают с HTTP и дополнительно содержат SSEPath и
MessagePath:

~~~go
type Config struct {
    Address      string
    SSEPath      string
    MessagePath  string
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func DefaultConfig() Config
func NewTransport(config Config) *Transport
func NewHandler(server *mcp.Server, config Config) (*Handler, error)
~~~

Оба HTTP-пакета экспортируют одинаковый helper для управляемого listener:

~~~go
import stdhttp "net/http"

type ServerConfig struct {
    Address      string
    Handler      stdhttp.Handler
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
}

func Server(ctx context.Context, config ServerConfig) *stdhttp.Server
~~~

Server возвращает обычный net/http.Server; вы сами вызываете ListenAndServe,
а библиотека вызывает Shutdown после отмены context.

## 10. Конфигурация

Пакет go.osspkg.com/mcp/config поддерживает два взаимоисключающих источника.
Выберите один loader и затем передайте результат в Transports.

~~~go
cfg, err := config.LoadEnv()
// или: cfg, err := config.LoadYAML("mcp.yaml")
if err != nil {
    log.Fatal(err)
}
transports, err := config.Transports(cfg, server, os.Stdin, os.Stdout)
~~~

Ключевые поля config.Config:

| Поле | Назначение |
| --- | --- |
| Name, Version, Instructions | identity и handshake |
| EnableStdio, EnableHTTP, EnableSSE | включение транспортов |
| HTTPAddress | адрес HTTP listener |
| MCPPath, SSEPath, MessagePath | URL paths |
| ReadTimeout, WriteTimeout, IdleTimeout | сетевые таймауты |
| MaxBodyBytes | лимит JSON body |
| SessionTTL, MaxSessions | параметры сессий |

config.Default() включает stdio, задаёт :8080, /mcp, /sse, /message, лимит body
1 MiB, TTL 30 минут и максимум 256 сессий.

Полная сигнатура конфигурации:

~~~go
type Config struct {
    Name         string
    Version      string
    Instructions string
    EnableStdio  bool
    EnableHTTP   bool
    EnableSSE    bool
    HTTPAddress  string
    MCPPath      string
    SSEPath      string
    MessagePath  string
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
    IdleTimeout  time.Duration
    MaxBodyBytes int64
    SessionTTL   time.Duration
    MaxSessions  int
}

func Default() Config
func LoadEnv() (Config, error)
func LoadYAML(path string) (Config, error)
func Transports(
    configuration Config,
    server *mcp.Server,
    input io.Reader,
    output io.Writer,
) ([]mcp.Transport, error)
~~~

Если stdio включён, input и output должны быть непустыми. При включённых HTTP
и SSE Transports создаёт один HTTP listener с обоими наборами endpoints.

### Environment

LoadEnv читает только известные ключи MCP_*, например:

~~~text
MCP_NAME=calculator
MCP_VERSION=1.0.0
MCP_ENABLE_STDIO=false
MCP_ENABLE_HTTP=true
MCP_HTTP_ADDRESS=:9090
MCP_MCP_PATH=/mcp
MCP_MAX_BODY_BYTES=1048576
MCP_SESSION_TTL=30m
~~~

Неизвестный ключ, пустое обязательное значение или неверное bool, integer или
duration возвращает ошибку.

### YAML

LoadYAML намеренно принимает только плоские scalar-записи:

~~~yaml
name: calculator
version: "1.0.0"
enable_stdio: false
enable_http: true
http_address: ":9090"
session_ttl: 30m
~~~

Вложенные maps, списки, anchors и неизвестные поля отклоняются. Это упрощает
валидацию и предотвращает неоднозначное слияние конфигурации.

## 11. Жизненный цикл и остановка

### Сигналы по умолчанию

Server.Run(transports ...) инкапсулирует:

~~~go
ctx, stop := signal.NotifyContext(
    context.Background(),
    os.Interrupt,
    syscall.SIGTERM,
)
defer stop()
return server.run(ctx, transports...)
~~~

Таким образом, Ctrl-C или SIGTERM отменяет общий контекст, после чего каждый
transport прекращает принимать новые запросы, закрывает активные сессии и
возвращает управление. Не создавайте второй signal handler вокруг Run, если вам
достаточно стандартного поведения.

### Управляемый контекст

Для embedding и тестов используйте:

~~~go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

go func() {
    _ = server.RunContext(ctx, transport)
}()

// ... работа приложения ...
cancel() // graceful shutdown
~~~

RunContext не устанавливает signal handler и уважает отмену вызывающего кода.
Для HTTP helper Server(ctx, config) shutdown вызывается автоматически; период
graceful shutdown ограничен пятью секундами.

## 12. Сценарии использования

### 12.1 Локальный CLI-агент

Используйте stdio, когда MCP-клиент запускает бинарник как дочерний процесс.
Оставьте EnableStdio=true, пишите логи только в stderr и регистрируйте
маленькие idempotent tools. Такой вариант не требует открытого порта и
аутентифицирует доступ границей процесса.

### 12.2 Удалённый HTTP-сервис

Включите Streamable HTTP, задайте явный HTTPAddress, MaxBodyBytes, таймауты и
middleware авторизации. Размещайте TLS и rate limiting на reverse proxy, а
внутри handler проверяйте bearer/session claims.

### 12.3 Legacy-клиент с SSE

Создайте NewTransportWithSSE, если часть клиентов ещё использует SSE. Пути
/mcp, /sse и /message должны быть различны. Ограничьте MaxSessions и
SessionTTL, чтобы отключившиеся браузеры не занимали память.

### 12.4 Динамические данные

Регистрируйте ResourceTemplate для файлов, документов или tenant-scoped данных.
Из ResourceRequest.Variables извлекайте только значения, соответствующие
шаблону, а доступ проверяйте middleware и внутри handler.

### 12.5 Контекстные подсказки

Используйте dynamic Prompt.Handler, когда сообщения зависят от аргументов,
пользователя или текущего состояния приложения. Не храните секреты в
PromptMessage: prompt возвращается MCP-клиенту.

### 12.6 Встраивание в существующий сервер

Используйте NewHandler и зарегистрируйте его в собственном http.ServeMux, если
приложение уже управляет listener, TLS и health endpoints. Для полного
контроля остановки передайте собственный context через RunContext либо
используйте mcphttp.Server(ctx, ...).

## 13. Ошибки и безопасность

Публичные sentinel errors:

~~~go
var (
    ErrUnauthorized = errors.New("unauthorized")
    ErrForbidden    = errors.New("forbidden")
    ErrStarted      = errors.New("server already started")
)
~~~

Рекомендации:

- ограничивайте HTTP body и SSE message body;
- задавайте конечные read/write/idle timeout;
- проверяйте авторизацию до чтения чувствительных resources;
- не возвращайте stack trace или токены из handler errors;
- не включайте CORS без явного списка origins;
- используйте URI template только после валидации path и tenant;
- не пишите отладочные данные в stdout stdio transport;
- передавайте ctx во внешние операции и немедленно обрабатывайте отмену.

## 14. Тестирование и качество

В репозитории предусмотрены:

~~~text
make lint
make tests
go test -race ./...
~~~

make lint проверяет форматирование, vet и статический анализ. make tests
запускает модульные и интеграционные тесты. Race-тест обязателен для изменений
реестра, сессий, shutdown и конкурентных handler-вызовов.

Для собственного инструмента тестируйте:

- schema и required-поля typed tool;
- duplicate names и попытку регистрации после старта;
- middleware для stdio, HTTP и SSE;
- malformed JSON-RPC, unknown method и invalid params;
- 401/403, body limits и неверный session id;
- отмену handler context и graceful shutdown;
- content type, SSE endpoint event и закрытие клиента.

## 15. Связанные материалы и изменения

- [README.md](README.md) — краткий обзор и минимальные примеры.
- [DOC.md](DOC.md) — English API reference and developer guide.
- [example/README.md](example/README.md) — готовые приложения stdio, HTTP,
  SSE, middleware и конфигурации.
- [AGENTS.md](AGENTS.md) — правила разработки и обязательные проверки.
- [skills/go-mcp/SKILL.md](skills/go-mcp/SKILL.md) — инструкция для AI-assisted
  разработки и ссылки на API/lifecycle/configuration references.
- [MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
  — нормативное описание транспортов.

### Changelog документации

| Дата | Изменение |
| --- | --- |
| 2026-09-19 | Для SSE добавлена фоновая очистка истёкших сессий и завершение связанных потоков. |
| 2026-09-19 | Добавлено подробное API-описание, сценарии использования и lifecycle guidance. |
