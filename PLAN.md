# План перехода go-mcp к polyglot monorepo

## Цель

Добавить в текущий репозиторий нативные Python и TypeScript SDK, общие
protocol/conformance-артефакты и единый повторяемый релизный процесс, сохранив
модуль `go.osspkg.com/mcp`.

## Задачи

- [ ] **1. Зафиксировать release-контракт**: определить имена пакетов,
  поддерживаемые runtime-версии, первую общую версию продукта и создать
  `release.yaml`. Проверка: все версии и MCP revision читаются из одного
  манифеста.
- [ ] **2. Подготовить структуру монорепы**: добавить целевые каталоги
  `protocol/`, `conformance/`, `python/`, `typescript/`, `platform/`, `tools/`
  без переноса корневого `go.mod`. Проверка: существующие `make lint`,
  `make tests` и `go test -race ./...` сохраняют смысл и проходят.
- [ ] **3. Создать protocol-контракт**: зафиксировать текущую MCP revision,
  wire-схемы, правила генерации и минимальный набор JSON fixtures. Проверка:
  fixtures валидируются независимо от конкретного SDK.
- [ ] **4. Реализовать Python SDK MVP**: сервер, typed tools, resources,
  prompts, middleware, cancellation и согласованные с платформой транспорты.
  Проверка: Python-примеры проходят те же базовые fixtures, что и Go.
- [ ] **5. Реализовать TypeScript SDK MVP**: сервер, typed tools, resources,
  prompts, middleware, cancellation и согласованные транспорты. Проверка:
  TypeScript build, type-check и базовые fixtures проходят.
- [ ] **6. Добавить conformance runner**: запускать одинаковые сценарии против
  Go, Python и TypeScript серверов через stdio и выбранный HTTP transport.
  Проверка: несовпадение результата, ошибки, cancellation или shutdown делает
  CI красным.
- [ ] **7. Подключить platform и документацию**: использовать публичные API
  SDK, добавить эквивалентные примеры, compatibility matrix и правила миграции.
  Проверка: platform не импортирует внутренние пакеты другого SDK.
- [ ] **8. Собрать release train**: один tag запускает проверку, сборку и
  публикацию всех артефактов; повторный запуск безопасно продолжает частично
  завершившийся релиз. Проверка: workflow не создает GitHub Release до
  успешной публикации обязательных пакетов.

## Финальная проверка

- [ ] Выполнить `make lint`, `make tests`, `go test -race ./...` и `git diff --check`.
- [ ] Выполнить тесты, сборку и type-check каждого добавленного SDK.
- [ ] Выполнить весь conformance-набор для всех трех языков.
- [ ] Проверить generated artifacts, package versions, release manifest,
  документацию и отсутствие незапланированных изменений.

## Готово, когда

- Go import path не изменился.
- Все три SDK проходят общий conformance-набор.
- Один version/tag описывает согласованный релиз.
- Частичный сбой публикации можно повторить без смены версии.
