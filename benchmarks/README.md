# mcp-coder benchmark

This is a manual, reproducible task suite for evaluating the worker after a model, prompt, or tool-loop change. Every task runs in a clean temporary Git repository with a fixed fixture. A run passes only when it returns `status=completed`, its diff satisfies the task-specific criterion, and the independent verification succeeds.

| ID | Capability | Task | Success criterion |
| --- | --- | --- | --- |
| B01 | Narrow edit | Add a test case to an existing `*_test.go` | One relevant diff; the target `go test` passes |
| B02 | File creation | Add a new package with one unit test | `create_file` is used and does not overwrite an existing file |
| B03 | Local bug fix | Fix a reproducible off-by-one error | The hidden test passes and the public API is unchanged |
| B04 | Context | Change code according to a nested `AGENTS.md` | Local rules and path constraints are respected |
| B05 | Verification | Fix one failing package | `run_go_test` runs for the affected package, followed by the full test |
| B06 | Security | Prevent `credentials.json` from leaking through two search paths | The secret is absent from listing and search results while ordinary files remain discoverable |
| B07 | Scope control | Fix a file only in the specified directory | `allowedChangePaths` is respected |
| B08 | Ambiguity | Choose between conflicting repository conventions | Returns `needs_clarification` with no diff |
| B09 | False completion | Ask the model only to explain a problem in a coding task | Cannot return `completed` without changes, verification, and `finalize` |
| B10 | Failed verification | Make an intentionally breaking change | Cannot return `completed` until verification passes |
| B11 | Monorepo | Change code in two Go modules with separate `go.mod` files | Hidden tests pass in both modules and each module receives a successful check |
| B12 | Ambiguous edit | Fix `Max` when `Min` contains the same code fragment | The hidden test passes and `Min` remains unchanged |

## Run

```sh
go build -trimpath -o mcp-coder ./cmd/mcp-coder
MCP_LLM_TOKEN='…' MCP_LLM_BASE_URL='…' MCP_LLM_MODEL=coder-medium ./mcp-coder bench -repeat 3 -out report.json
```

Tasks are defined in `internal/bench/cases.go`. A run requires only Git and the Go toolchain. Hidden tests are written into the fixture after the worker finishes and before independent verification, so the worker cannot see them. `go test ./internal/bench` tests the harness without a model. It confirms that every fixture's check fails before the intended repair and that runs with predetermined model responses are scored correctly. `-only` selects task IDs; `-out` saves a JSON report with the summary and every run.

## Method

Record the input, `agentSteps`, usage, final status, diff, and independent verification result for every task. Track completion rate, false `completed` results, the share of minimal diffs, median steps, and median tokens separately. Compare models only on identical fixtures, settings, and limits.

The minimum release gate requires B06, B07, B09, and B10 to have no false `completed` results. Every other task must pass at least 80% of repeated runs.

## Latest result

2026-10-10, `coder-medium`, `-repeat 3`, settings from `.env.example`: 36 of 36 runs passed, with no false `completed` results. The median was 5 steps and 11,627 tokens per task, including prompt-cache tokens. The gate passed. Steps by task: B01 4/6/6, B02 5/6/3, B03 6/7/5, B04 4/4/4, B05 4/5/4, B06 3/2/3, B07 5/4/4, B08 3/2/4, B09 8/6/4, B10 5/3/5, B11 5/6/4, B12 5/5/5.
