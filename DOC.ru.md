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
   - [API регистрации и взаимодействие с LLM](#api-регистрации-и-взаимодействие-с-llm)
5. [Typed tools и JSON Schema](#5-typed-tools-и-json-schema)
6. [Resources и prompts](#6-resources-и-prompts)
7. [Middleware и авторизация](#7-middleware-и-авторизация)
8. [JSON-RPC и методы MCP](#8-json-rpc-и-методы-mcp)
9. [Транспорты](#9-транспорты)
   - [MCP-клиент](#mcp-клиент)
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
| mcp/client | stdlib-only MCP-клиент и клиентские транспорты |

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
    Title        string
    Version      string
    Description  string
    Instructions string
    WebsiteURL   string
    Icons        []Icon
}

type Option func(*Server) error

func New(info ServerInfo, options ...Option) (*Server, error)
func WithMiddleware(middleware ...Middleware) Option
func WithCapabilities(capabilities map[string]any) Option
~~~

Name и Version обязательны. Instructions передаются клиенту в ответе
initialize. WithMiddleware сохраняет порядок аргументов: первый middleware в
списке получает запрос первым.

`WithCapabilities` добавляет поля в объект `capabilities` ответа
`initialize`. Он нужен для application-defined/experimental возможностей или
для явной настройки стандартной capability:

~~~go
server, err := mcp.New(info, mcp.WithCapabilities(map[string]any{
    "experimental": map[string]any{
        "vendor.example": map[string]any{"streaming": true},
    },
    "tools": map[string]any{"listChanged": true},
}))
~~~

Карта копируется при создании сервера, поэтому её последующее изменение
вызывающим кодом безопасно. Во время `initialize` встроенные capabilities
каталога, logging и Tasks добавляются только если соответствующий верхний
уровень не задан. Переданные верхнеуровневые значения имеют приоритет и не
объединяются глубоко.

New возвращает ошибку для пустых обязательных полей, nil option или nil
middleware. Сервер не готовит транспорт сам: транспорт передаётся в Run.

### Запрос и обработчик

~~~go
type RequestMeta struct {
    Transport string
    Headers   map[string]string
    SessionID string
    Notify    NotificationSender
    Call      ClientCaller
}

type Request struct {
    Method string
    Params json.RawMessage
    Meta   RequestMeta
}

type Handler func(context.Context, Request) (any, error)
type Middleware func(Handler) Handler
func RequestFromContext(context.Context) (Request, bool)
~~~

RequestMeta.Transport имеет значение stdio, http или sse. HTTP и SSE передают
копию входящих заголовков и идентификатор MCP-сессии. Middleware может получить
метод из Request.Method, а отмену — из context.Context. `RequestFromContext`
позволяет typed handler отправлять progress/logging notifications и выполнять
sampling, roots и elicitation-запросы к клиенту.

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
func WithRequestObserver(observers ...RequestObserver) Option
~~~

Имена tools/prompts и URI ресурсов должны быть уникальными в своей категории.
При первом запросе реестр фиксируется. После этого регистрация возвращает
ErrStarted; это позволяет безопасно обслуживать запросы конкурентно и
гарантирует стабильные ответы */list.

### API регистрации и взаимодействие с LLM

Регистрация добавляет возможность в каталог сервера. Она не вызывает LLM и не
передаёт Go-функцию непосредственно модели. MCP-клиент подключается к серверу,
получает каталог через JSON-RPC и сам решает, какие записи показать модели.

| API | Что регистрируется | Метод обнаружения | Метод запроса | Для чего подходит |
| --- | --- | --- | --- | --- |
| `RegisterResource` | Фиксированный URI и его содержимое | `resources/list` | `resources/read` | Конфигурация, документация или другой стабильный документ |
| `RegisterResourceTemplate` | Шаблон URI и callback | `resources/templates/list` | `resources/read` с конкретным URI | Файлы, записи и другие данные, адресуемые переменными |
| `RegisterPrompt` | Повторно используемый prompt и его аргументы | `prompts/list` | `prompts/get` | Управляемые сценарии и повторяемые инструкции |
| `RegisterTool` | Вызываемая операция с типизированными input/output | `tools/list` | `tools/call` | Действия, расчёты и интеграции |

Регистрируйте все записи при создании сервера, до вызова `Run` или обработки
первого запроса:

~~~go
server, err := mcp.New(mcp.ServerInfo{Name: "catalog", Version: "1.0.0"})
if err != nil {
    return err
}
if err := server.RegisterResource(resource); err != nil {
    return err
}
if err := server.RegisterResourceTemplate(template); err != nil {
    return err
}
if err := server.RegisterPrompt(prompt); err != nil {
    return err
}
if err := mcp.RegisterTool(server, "lookup", "Ищет запись", lookup); err != nil {
    return err
}
return server.Run(transport)
~~~

Первый запрос фиксирует этот каталог. Поэтому регистрацию следует держать в
startup-коде: поздняя регистрация возвращает `mcp.ErrStarted`. Типичный обмен
между LLM и MCP-сервером выглядит так:

1. MCP-клиент отправляет `initialize` и получает capabilities сервера.
2. Клиент запрашивает `tools/list`, `resources/list`,
   `resources/templates/list` и `prompts/list`, если соответствующие
   возможности объявлены.
3. Host-приложение преобразует метаданные в контекст модели. Точный вид
   зависит от клиента: tools могут попасть в определения функций модели,
   prompts — в список команд, а resources могут читаться только при
   необходимости.
4. Модель выбирает действие в этом контексте. Она формирует вызов tool,
   просит host прочитать resource или выбирает prompt; сама Go-функция
   callback моделью не вызывается.
5. Клиент отправляет соответствующий MCP-запрос. go-mcp запускает middleware,
   сохраняет контекст запроса, проверяет и декодирует параметры и при
   необходимости вызывает зарегистрированный handler.
6. Клиент возвращает ответ host-приложению. Host добавляет результат или
   сообщения prompt в диалог, после чего модель продолжает рассуждение или
   отвечает пользователю.

Иными словами, регистрация задаёт MCP-контракт сервера, клиент является
границей протокола, а LLM — одним из потребителей метаданных и результатов.
Модель не получает автоматически все resources и не выполняет каждый prompt:
момент и способ их предоставления контролирует host/client.

#### `RegisterResource`: стабильный контекст

Используйте `RegisterResource`, когда URI известен заранее, а содержимое
доступно одним значением. Resource — это данные, а не операция:

~~~go
err := server.RegisterResource(mcp.Resource{
    URI:         "config://application",
    Name:        "Конфигурация приложения",
    Description: "Несекретные настройки runtime",
    MIMEType:    "application/json",
    Text:        `{"environment":"production","region":"eu"}`,
})
~~~

В `resources/list` сервер публикует только метаданные (`uri`, `name`,
`description` и `mimeType`). Когда клиенту нужно содержимое, он отправляет:

~~~json
{"jsonrpc":"2.0","id":7,"method":"resources/read",
 "params":{"uri":"config://application"}}
~~~

Ответ содержит `contents` с URI, MIME type и текстом. Host может добавить этот
текст в контекст модели, процитировать его в ответе или не показывать модели.
`RegisterResource` подходит для схемы, README, политики или сгенерированного
снимка, у которого должен быть один стабильный адрес. Не помещайте credentials
и другие секреты в resource, если middleware и политика контекста host явно не
защищают их.

#### `RegisterResourceTemplate`: данные с переменными в URI

Используйте `RegisterResourceTemplate`, когда один логический resource имеет
много конкретных URI или должен вычисляться во время чтения. Шаблон публикуется
как pattern, а callback запускается только после запроса клиента с подходящим
URI:

~~~go
err := server.RegisterResourceTemplate(mcp.ResourceTemplate{
    URITemplate: "record:///{id}",
    Name:        "Запись клиента",
    Description: "Запись клиента, выбранная по id",
    MIMEType:    "application/json",
    Handler: func(ctx context.Context, request mcp.ResourceRequest) (mcp.Resource, error) {
        id := request.Variables["id"]
        record, err := loadRecord(ctx, id)
        if err != nil {
            return mcp.Resource{}, err
        }
        return mcp.Resource{
            URI:      request.URI,
            Name:     "Клиент " + id,
            MIMEType: "application/json",
            Text:     record,
        }, nil
    },
})
~~~

Текущий matcher сравнивает сегменты, разделённые slash. Placeholder вроде
`{id}` соответствует одному сегменту; извлечённые значения передаются в
`ResourceRequest.Variables`, а исходный URI сохраняется в
`ResourceRequest.URI`. Callback получает контекст запроса, поэтому отмена
может остановить медленный поиск. Проверяйте идентификаторы и права доступа в
callback или middleware до обращения к хранилищу: URI template задаёт
маршрутизацию, но не является правилом авторизации.

Модель обычно не изобретает прямой вызов Go-callback. Она видит метаданные
шаблона через `resources/templates/list`; host может разрешить запрос
конкретного URI, например `record:///42`. После этого клиент отправляет
`resources/read`, а сервер разрешает шаблон.

#### `RegisterPrompt`: повторно используемый сценарий диалога

Используйте `RegisterPrompt` для именованного повторяемого сценария инструкций.
Prompt выбирается пользователем или UI host и возвращается как сообщения; это
не tool и не императивная операция.

Статический prompt хранит сообщения непосредственно в каталоге:

~~~go
err := server.RegisterPrompt(mcp.Prompt{
    Name:        "summarize",
    Description: "Запросить краткое резюме",
    Arguments: []mcp.PromptArgument{
        {Name: "style", Description: "formal или casual", Required: true},
    },
    Messages: []mcp.PromptMessage{
        {Role: "user", Text: "Кратко суммируй переданный документ."},
    },
})
~~~

Динамический prompt использует `Handler`, чтобы формировать сообщения для
каждого запроса `prompts/get`:

~~~go
err := server.RegisterPrompt(mcp.Prompt{
    Name:        "review",
    Description: "Подготовить code review",
    Arguments: []mcp.PromptArgument{
        {Name: "language", Description: "Язык программирования", Required: true},
    },
    Handler: func(ctx context.Context, arguments map[string]string) ([]mcp.PromptMessage, error) {
        language := arguments["language"]
        if language == "" {
            return nil, errors.New("language is required")
        }
        return []mcp.PromptMessage{
            {Role: "user", Text: "Проверь этот код на " + language + " на корректность и безопасность."},
        }, nil
    },
})
~~~

`prompts/list` публикует имя, описание и метаданные аргументов. При выборе
prompt клиент отправляет `prompts/get` со строковыми аргументами. Сервер
возвращает сообщения с ролями и текстом; host может вставить их в диалог и
передать управление модели. `Required` документирует ожидаемый ввод для
клиентов, но dynamic handler всё равно должен проверять аргументы: host может
отправить неполную map.

#### `RegisterTool`: операция, которую может вызвать модель

Используйте `RegisterTool` для операции с побочными эффектами, вычислением или
внешней интеграцией. В отличие от трёх остальных API, это регистрация действия.
Generic input/output делают wire-контракт явным:

~~~go
type LookupInput struct {
    ID string `json:"id" mcp:"description=Идентификатор клиента"`
}

func (value *LookupInput) UnmarshalJSON(data []byte) error {
    type alias LookupInput
    var decoded alias
    if err := json.Unmarshal(data, &decoded); err != nil {
        return err
    }
    *value = LookupInput(decoded)
    return nil
}

type LookupOutput struct {
    Name  string `json:"name"`
    Level string `json:"level"`
}

func (value *LookupOutput) MarshalJSON() ([]byte, error) {
    type alias LookupOutput
    return json.Marshal(alias(*value))
}

err := mcp.RegisterTool(server, "lookup", "Ищет клиента по идентификатору",
    func(ctx context.Context, input *LookupInput) (*LookupOutput, error) {
        return lookupCustomer(ctx, input.ID)
    })
~~~

При регистрации go-mcp строит JSON Schema input из тегов `json` и `mcp`. В
`tools/list` клиент получает имя tool, описание и `inputSchema`; host может
передать эту схему модели. Тогда модель способна сформировать структурированный
вызов:

~~~json
{"jsonrpc":"2.0","id":8,"method":"tools/call",
 "params":{"name":"lookup","arguments":{"id":"42"}}}
~~~

Сервер декодирует JSON, запускает цепочку middleware и вызывает typed handler
с контекстом запроса. Успешный результат возвращается одновременно в
`structuredContent` и как JSON text content, поэтому его могут использовать
клиенты, работающие со структурированным выводом, и клиенты, понимающие только
текст. Ошибка декодирования или handler возвращается как безопасный tool
result с `isError: true`; детали реализации модели не раскрываются.

Модель сама решает, вызывать ли tool. Описание с условиями применимости,
точные описания полей и осторожное описание side effects помогают ей выбрать
операцию безопасно. Авторизация должна находиться в middleware или коде
приложения: описание tool не является механизмом безопасности.

`RegisterToolWithOptions` добавляет метаданные MCP 2025-11-25: icons,
annotations и outputSchema. Если outputSchema не задан явно, он выводится из
типизированного результата.

Клиентские sampling, roots, elicitation, progress и logging доступны внутри
typed handler через `RequestFromContext(ctx)`. Методы `tasks/get`,
`tasks/result` и `tasks/cancel` поддерживают experimental durable tasks:
добавьте `"task":{"ttl":60000}` к `tools/call`, а затем опрашивайте handle.

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
| resources/subscribe, resources/unsubscribe | подписки на обновления resource |
| prompts/list | список prompts и аргументов |
| prompts/get | получение сообщений prompt |
| logging/setLevel | выбор минимального уровня логирования |
| tasks/get, tasks/result, tasks/cancel, tasks/list | состояние и результаты durable task |

Ответ initialize объявляет protocol version 2025-11-25 и capabilities
tools/resources/prompts/logging/tasks. Неизвестный метод получает -32601, неверные параметры
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

GET /mcp с Mcp-Session-Id открывает text/event-stream. Уведомления сервера и
server-initiated requests получают ограниченные session-scoped event IDs.
Клиент может переподключиться с Last-Event-ID и получить сохранённые события.
POST принимает коррелированные JSON-RPC responses для server-initiated requests.
Задайте явный allowlist в AllowedOrigins: непустой Origin вне списка получает
403.

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

### Discovery OAuth и OpenID Connect

Core-пакет только моделирует discovery-документы и не выпускает токены и не
подключает authentication provider. `mcp.ProtectedResourceMetadata` описывает
защищённый resource по RFC 9728, а `mcp.AuthorizationServerMetadata` —
authorization server или OIDC discovery. HTTP-пакет предоставляет handlers
`NewProtectedResourceMetadataHandler` и
`NewAuthorizationServerMetadataHandler`.

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
При остановке transport все активные сессии закрываются и cleanup завершается
сразу. Если SSE handler смонтирован в HTTP server приложения, вызовите `Close`,
когда он больше не нужен.

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
func (h *Handler) Close()
~~~

`Content-Type: application/json` может содержать параметры, например
`charset=utf-8`. JSON-RPC ID допускает строку, число или null; параметры MCP
метода должны быть объектом.

### Лимиты stdio и наблюдение запросов

`stdio.NewTransport` сохраняет лимит строки 1 MiB. Для явного положительного
лимита используйте `stdio.NewTransportWithConfig` или `stdio.ServeWithConfig`
с `stdio.Config{MaxMessageBytes: ...}`. `WithRequestObserver` вызывается после
обработки каждого разобранного запроса и получает его длительность; hook должен
выполняться быстро. Паника observer изолируется и не может завершить обработку
запроса.

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
а библиотека вызывает Shutdown после отмены непустого context. При nil context
остановкой управляет вызывающий код.

### 9.5 MCP-клиент

`go.osspkg.com/mcp/client` — stdlib-only клиент для всех транспортов этой
библиотеки. Создайте transport, затем `client.Client`, запустите его с
контекстом жизненного цикла и завершите MCP handshake до вызова каталогов:

~~~go
transport, err := client.NewHTTPTransport("http://127.0.0.1:8080/mcp", client.HTTPConfig{})
if err != nil { log.Fatal(err) }
mcpClient, err := client.New(transport)
if err != nil { log.Fatal(err) }
defer mcpClient.Close()
if err := mcpClient.Start(ctx); err != nil { log.Fatal(err) }
_, err = mcpClient.Initialize(ctx, client.InitializeParams{})
if err != nil { log.Fatal(err) }
tools, err := mcpClient.ListTools(ctx)
if err != nil { log.Fatal(err) }
~~~

Для запуска дочернего процесса используйте
`NewCommandTransport("./my-mcp-server", args, CommandConfig{})`; для уже
открытых потоков — `NewStdioTransport(input, output, StdioConfig{})`, а для
legacy-транспорта —
`NewSSETransport("http://host/sse", SSEConfig{})`. Typed helpers включают
`ListResources`, `ReadResource`, `ListPrompts`, `GetPrompt`, `CallTool` и
операции Tasks. Для новых или прикладных методов остаётся `CallRaw`.
Ошибки создания и запуска команды `Client.Start` возвращает синхронно.
Дочерний процесс получает заданные рабочий каталог и окружение, пишет
диагностику в `CommandConfig.Stderr` и завершается при отмене контекста клиента
или вызове `Close`.

Server-to-client методы подключаются явно, чтобы приложение само определяло
разрешённые возможности:

~~~go
_ = mcpClient.OnRequest("roots/list", func(context.Context, json.RawMessage) (any, error) {
    return map[string]any{"roots": []any{}}, nil
})
_ = mcpClient.OnRequest("sampling/createMessage", samplingHandler)
_ = mcpClient.OnNotification("notifications/progress", progressHandler)
~~~

Клиент сопоставляет параллельные запросы по JSON-RPC ID, ограничивает ответы
HTTP/SSE, сериализует записи stdio, по умолчанию ограничивает обработчики
server-to-client сообщений восемью и отменяет ожидающие вызовы при остановке
транспорта. Для другого положительного предела используйте
`ClientConfig.MaxConcurrentHandlers`; лишние server requests получают внутреннюю
ошибку, а лишние notifications отбрасываются. Streamable HTTP принимает как JSON,
так и SSE-ответы на POST. Message endpoint legacy SSE обязан остаться на origin
настроенного SSE endpoint. Аутентификация и TLS остаются политикой приложения:
передайте настроенный `http.Client` или заголовки в конфигурацию транспорта.

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
| 2026-09-19 | Добавлен ленивый запуск stdio-сервера как дочернего процесса с остановкой по context и отдельным stderr. |
| 2026-09-19 | Добавлены stdlib-only MCP-клиент и runnable client example для stdio, Streamable HTTP и legacy SSE. |
| 2026-09-19 | Для SSE добавлена фоновая очистка истёкших сессий и завершение связанных потоков. |
| 2026-09-19 | Добавлено подробное API-описание, сценарии использования и lifecycle guidance. |
| 2026-09-19 | Закрытие SSE handler теперь немедленно завершает активные сессии и cleanup. |
| 2026-09-19 | Добавлены строгая проверка запросов, URI variables, лимиты stdio, observers, fuzzing и benchmarks. |
