# mcp-coder

`mcp-coder` — локальный MCP-сервер со stdio-транспортом. Он делегирует ограниченные, чётко сформулированные задачи модели, совместимой с Anthropic Messages API. Claude Code или Codex остаётся основной моделью: он планирует работу и проверяет результат; бинарник выступает только самостоятельным исполнителем.

## Сборка

```bash
go build -trimpath -o mcp-coder ./cmd/mcp-coder
MCP_LLM_TOKEN='…' MCP_LLM_BASE_URL='https://llm.corp.ru/api' MCP_LLM_MODEL=coder-medium ./mcp-coder doctor
```

Процессу необходимы `MCP_LLM_TOKEN`, `MCP_LLM_BASE_URL` и `MCP_LLM_MODEL`. Запросы направляются на `${MCP_LLM_BASE_URL}/v1/messages`; название модели не зашито в код. Для переключения модели пересборка не нужна:

```bash
MCP_LLM_MODEL=coder ./mcp-coder
MCP_LLM_MODEL=coder-mini ./mcp-coder
```

Доступные ограничения: `MCP_MAX_AGENT_STEPS` (16), `MCP_MAX_INPUT_CHARS` (12000), `MCP_MAX_FILE_CHARS` (24000), `MCP_MAX_TOTAL_CONTEXT_CHARS` (100000), `MCP_MAX_OUTPUT_TOKENS` (1600), `MCP_REQUEST_TIMEOUT_MS` (60000) и `MCP_COMMAND_TIMEOUT_MS` (60000).

## Claude Code

Не меняйте `ANTHROPIC_BASE_URL` и `ANTHROPIC_MODEL` у Claude Code. Добавьте сервер как отдельный MCP-инструмент; переменные `MCP_*` будут доступны только дочернему процессу:

```bash
claude mcp add mcp-coder --env MCP_LLM_TOKEN --env MCP_LLM_BASE_URL --env MCP_LLM_MODEL -- /absolute/path/mcp-coder
```

## Codex

Добавьте stdio MCP server в конфигурацию Codex (точный интерфейс настройки зависит от клиента):

```toml
[mcp_servers.mcp-coder]
command = "/absolute/path/mcp-coder"

[mcp_servers.mcp-coder.env]
MCP_LLM_TOKEN = "..."
MCP_LLM_BASE_URL = "https://llm.corp.ru/api"
MCP_LLM_MODEL = "coder-medium"
```

`execute_coding_task` предназначен для реализации задачи, а `ask_coder` — для коротких запросов только на чтение. По умолчанию coding-задача не может завершиться успешно без наблюдаемого изменения файла, успешной проверки и tool-вызова `finalize`. Для исследовательской задачи укажите `readOnly: true`; при необходимости контракт можно явно уточнить полями `requireChanges`, `requireVerification` и `allowedChangePaths`.

У исполнителя есть безопасные `create_file` и `edit_file`, а также `run_go_test` для одного относительного Go-пакета (`./internal/tools`) или всего модуля (`./...`). Произвольный shell по-прежнему недоступен.

`execute_coding_task` дополнительно принимает необязательный `projectContext`: краткий hint основной модели, например запрет на legacy-подход. Он не заменяет самостоятельное исследование проекта. Перед изменением executor обнаруживает только известные instruction-файлы (`AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md` и релевантные README), учитывает вложенные правила по пути suggested files и передаёт модели небольшой список соседних файлов для поиска уже существующих patterns. Явные constraints задачи имеют приоритет над `projectContext`, затем применяются instructions и локальный код.

Если активные conventions конфликтуют и безопасно выбрать подход нельзя, результат имеет статус `needs_clarification`; основной модели нужно принять решение. В успешном результате поле `conventionsUsed` содержит только пути применённых правил и соседних patterns, без их содержимого.

Внутри бинарника MCP adapter вызывает независимый executor core. Core можно использовать из будущего UI без изменения agent loop. Он выдаёт lightweight in-process events (`task.started`, discovery, agent/tool, file, verification и final status); сейчас они не требуют consumer, не создают background process и не записываются на диск.

## Безопасность и ограничения

Все файловые операции проходят через единый resolver workspace: абсолютные пути, traversal, выход через symbolic link и распространённые secret-файлы запрещены. У агента нет shell-инструмента. Проверки запускаются только из точного allowlist, массивом аргументов и с timeout. Git используется только для `status` и `diff`: сервер не делает commit, reset, restore или clean. Размеры файлов, вывода, контекста, число шагов и повторы API ограничены. Логи пишутся только в stderr и не содержат учётных данных.

`doctor` проверяет локальную конфигурацию без сети. `doctor --api` выполняет opt-in безопасную проверку Messages API и tool-use roundtrip с synthetic tool; файловые, Git и verification-инструменты при этом не используются. Токен не выводится.
