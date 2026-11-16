# mcp-coder

A local stdio MCP server that delegates well-scoped coding, testing, and documentation tasks to a separate model. It works with any model whose API is compatible with Anthropic Messages. The primary model (Claude Code, Codex) plans and reviews; `mcp-coder` implements. This keeps implementation details out of the primary model's context and is designed to reduce its token usage. Total token usage across both models may be higher for small tasks.

## Agent orchestration skill

Want Claude Code, Codex, Cursor, or Copilot to turn requests into bounded worker contracts, parallelize only disjoint edits, and review verification before reporting success? [MCP Coder Delegator](https://www.agensi.io/skills/mcp-coder-delegator) packages that workflow as an installable premium skill.

## Installation

Download a ready-to-run archive for macOS, Linux, or Windows from [GitHub Releases](https://github.com/PoppieHub/mcp-coder/releases), or build from source:

```bash
go build -trimpath -o mcp-coder ./cmd/mcp-coder
MCP_LLM_TOKEN='…' MCP_LLM_BASE_URL='https://llm.example.com/api' MCP_LLM_MODEL=coder-medium ./mcp-coder doctor
```

`doctor` checks the configuration without network access. `doctor --api` also sends a request to the model and exercises tool use.

**Claude Code.** Do not modify `ANTHROPIC_*`; the server connects as a separate tool.

```bash
claude mcp add mcp-coder --env MCP_LLM_TOKEN --env MCP_LLM_BASE_URL --env MCP_LLM_MODEL -- /absolute/path/mcp-coder
```

**Claude Desktop.** `scripts/package-claude-dxt.sh` builds `dist/mcp-coder.dxt`. Install it through **Settings → Extensions → Advanced settings → Install Extension…** and enter the token in the form. The package is built for the architecture of the current Mac.

**Codex.**

```toml
[mcp_servers.mcp-coder]
command = "/absolute/path/mcp-coder"

[mcp_servers.mcp-coder.env]
MCP_LLM_TOKEN = "..."
MCP_LLM_BASE_URL = "https://llm.example.com/api"
MCP_LLM_MODEL = "coder-medium"
```

## Configuration

`MCP_LLM_TOKEN`, `MCP_LLM_BASE_URL` (requests go to `${MCP_LLM_BASE_URL}/v1/messages`), and `MCP_LLM_MODEL` are required. Everything else is optional. Values outside the supported range fall back to the default.

| Variable | Default | Range | Purpose |
| --- | --- | --- | --- |
| `MCP_MAX_AGENT_STEPS` | 32 | 1–50 | model steps per task |
| `MCP_MAX_PARALLEL_TASKS` | 2 | 1–8 | concurrently running tasks |
| `MCP_TASK_TIMEOUT_MS` | 600000 | 1000–3600000 | total task timeout |
| `MCP_REQUEST_TIMEOUT_MS` | 60000 | 1000–600000 | model request timeout |
| `MCP_COMMAND_TIMEOUT_MS` | 60000 | 1000–600000 | verification command timeout |
| `MCP_MAX_INPUT_CHARS` | 12000 | 256–100000 | maximum task input length |
| `MCP_MAX_FILE_CHARS` | 24000 | 1024–200000 | maximum size of one `read_file` response; large files are read in windows |
| `MCP_MAX_TOTAL_CONTEXT_CHARS` | 160000 | 4096–1000000 | conversation size that triggers compression of older tool output |
| `MCP_MAX_OUTPUT_TOKENS` | 3000 | 128–16000 | maximum tokens in a model response |
| `MCP_PROMPT_CACHE` | `true` | `true`/`false` | prompt cache markers |

`update_settings` changes the model, gateway URL, step limit, parallel task limit, and timeouts until the server restarts.

## Tools

| Tool | Purpose |
| --- | --- |
| `execute_coding_task` | execute a task and return its result |
| `start_coding_task` / `wait_task` | run the same task in the background; start several tasks and then collect each result |
| `get_status` / `cancel_task` | inspect running tasks, steps, phases, and claimed files / cancel one task by `taskId` or all tasks |
| `describe_project` | return the workspace and target-module stack, verification commands, and required checks without calling the model |
| `ask_coder` | ask a short read-only question; files listed in `files` are attached to the question |
| `get_settings` / `update_settings` / `run_diagnostics` | inspect settings, update runtime settings, and run diagnostics |

Task input:

- `task` — the requested outcome;
- `workspaceRoot`;
- `suggestedFiles`, `allowedChangePaths`;
- `constraints`;
- `projectContext` — facts already discovered, so the worker does not repeat the investigation;
- `verification` and `verificationDir` — the check to run and the module where it runs;
- `readOnly`, `requireChanges`, `requireVerification`.

Result:

- `status`: `completed`, `failed`, or `needs_clarification` (the task cannot be completed as specified and needs a decision from the primary model);
- `summary`, `changedFiles`, `verification`, `warnings`, `usage`, `agentSteps`;
- `diff` for changed and created files, regardless of the outcome;
- `failedVerification` — the last failed check with the tail of its output when the task does not complete.

## How the worker operates

- **Context.** Before starting, the worker reads `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, and README files along the paths to the task files. It derives the root and target-module stacks from manifests (`go.mod`, `package.json` with its package manager, `Cargo.toml`, `pyproject.toml`, `Makefile`, `justfile`, and `Taskfile.yml`).
- **File ownership.** For `.go`, the module is the nearest `go.mod`; for `.ts`/`.js`, it is the nearest `package.json`; and similarly for other languages. If no language manifest exists, the nearest `Makefile`, `justfile`, or `Taskfile` is used, otherwise the workspace root.
- **Verification.** When `verification` is omitted, every module containing changed code must have a successful check. The worker selects checks from the detected stack: a narrow `go test` for changed packages or a declared `test`, `check`, or `verify` command. A backend-and-frontend task cannot complete until both are verified.
- **Allowed commands.** Verification is limited to commands declared by the repository (`package.json` scripts and `make`/`just`/`task` targets) or standard toolchain commands (`go test|vet|build`, `cargo test|clippy|check|build`, `pytest`, `tox`). Arguments may only refer to paths inside the workspace. A command without its runner (`test ./...`, `verify`) is expanded when there is one unambiguous valid form. A Go check requested outside a module is routed to the module where the task changed Go code.
- **Edits.**
  - `edit_file` applies a set of replacements atomically, and each replacement must match exactly once.
  - `create_file`, `delete_file`, and `rename_file` never overwrite an existing file.
  - Go and JSON syntax is checked immediately after an edit.
- **Completion.** `finalize` audits the task contract. Changed files and verification results come from recorded actions, not from the model's report.
- **Parallel tasks in one workspace.**
  - a task whose `allowedChangePaths` overlap a running task is rejected before it starts;
  - a file claimed by one task cannot be changed by another;
  - verification commands run sequentially.

  Tasks in separate modules parallelize best.
- **Model requests.** Prompts sent to the worker model are in English. Prompt caching is used when the gateway supports it. A 429 response is retried after the `Retry-After` delay.

## Security

- Every path passes through one resolver. Absolute paths, `..`, symlink escapes, and secret files (`.env`, keys, credentials) are rejected.
- There is no shell. Verification commands run as argument arrays, with a timeout, and only from the allowlist.
- Git is read-only (`status`, `diff`); the server never commits, resets, or cleans.
- The token never appears in responses or logs. Logs go to stderr.

## Benchmark

```bash
./mcp-coder bench -repeat 3 -out report.json -trace traces/
```

This runs the tasks in [`benchmarks/README.md`](benchmarks/README.md) against the model configured in the environment. Every task runs in a temporary Git repository and is scored by hidden tests rather than the worker's report. `-trace` stores the conversation for each run. Exit code 1 means the release gate failed.
