# Delegation Playbook

Read the relevant section when the main workflow needs a concrete contract example, parallel task split, recovery decision, or prerequisite setup.

## Contract example: localized bug fix

Use the tool fields rather than embedding the whole contract in prose:

```yaml
task: >-
  Fix duplicate retry scheduling after a successful request. Preserve the public
  API. Add a regression test that fails before the fix and passes afterward.
workspaceRoot: /absolute/path/to/repository
suggestedFiles:
  - internal/client/retry.go
  - internal/client/retry_test.go
allowedChangePaths:
  - internal/client/retry.go
  - internal/client/retry_test.go
constraints:
  - Do not change exported signatures.
  - Preserve cancellation behavior.
projectContext: >-
  scheduleRetry is called from send; the duplicate begins after success followed
  by deferred cleanup. Existing tests use a fake clock.
verification: go test ./internal/client
verificationDir: .
requireChanges: true
requireVerification: true
```

A good `task` states the result. `projectContext` carries known facts. `constraints` protects invariants. Avoid putting guesses into any field.

## Contract example: tests for existing behavior

```yaml
task: >-
  Add table-driven tests for parseLimit covering the documented default, minimum,
  maximum, malformed input, and values outside the allowed range. Do not change
  production behavior.
suggestedFiles:
  - internal/config/config.go
  - internal/config/config_test.go
allowedChangePaths:
  - internal/config/config_test.go
constraints:
  - Test observable behavior rather than private implementation details.
verification: go test ./internal/config
requireChanges: true
requireVerification: true
```

Notice that the production file is useful reading context but is not writable.

## Contract example: documentation-only change

```yaml
task: >-
  Update the configuration table to document REQUEST_TIMEOUT_MS, including its
  default, valid range, and one shell example. Keep the existing tone and layout.
suggestedFiles:
  - README.md
allowedChangePaths:
  - README.md
constraints:
  - Do not change source code.
requireChanges: true
requireVerification: false
```

Disable verification only when no meaningful executable check exists.

## Parallel partitioning

Parallel work is safe when tasks share intent but not writable state. Examples:

- backend handler and its backend tests in one task; independent frontend view and its frontend tests in another;
- separate packages with separate checks;
- documentation in one file and an isolated implementation in another, provided neither task regenerates shared output.

Keep work sequential when tasks touch any of the following:

- the same file or directory subtree allowed to both tasks;
- a shared interface, schema, migration chain, lockfile, snapshot, or generated file;
- behavior where the second task depends on the exact diff produced by the first;
- checks that mutate shared state or cannot safely run concurrently.

Start all safe tasks before waiting for the first. Waiting immediately after each start makes the work sequential.

## Recovery matrix

| Result | Primary-agent action |
| --- | --- |
| `completed`, clean diff, successful check | Review behavior and accept or perform proportional final validation. |
| `completed`, warning present | Resolve the warning before claiming success. |
| `needs_clarification` | Make the missing decision, narrow the contract, and resubmit once. |
| `failed`, verification failure | Inspect `failedVerification`; retry only with a concrete correction or fix locally. |
| `failed`, scope violation | Tighten paths and task wording; do not widen scope unless the solution truly requires it. |
| Partial changes after cancellation | Inspect the retained diff before continuing or assigning overlapping work. |
| Repeated unchanged failure | Stop delegating that task and handle it with the primary agent or ask the user for genuinely missing input. |

## Installation and configuration

Use this only when the host agent cannot see the mcp-coder tools.

The server source and current setup instructions are at:

`https://github.com/PoppieHub/mcp-coder`

Prebuilt archives for macOS, Linux, and Windows are available at:

`https://github.com/PoppieHub/mcp-coder/releases`

It requires a model API compatible with Anthropic Messages and these environment variables:

- `MCP_LLM_TOKEN`
- `MCP_LLM_BASE_URL`
- `MCP_LLM_MODEL`

After installation, run the server's `doctor` command locally before asking the agent to use this skill. Use `doctor --api` only when the user authorizes an API request that may incur provider usage.

Never request that a user paste a token into chat, source files, task text, or logs. Configuration belongs in the host's environment or secret-management UI.
