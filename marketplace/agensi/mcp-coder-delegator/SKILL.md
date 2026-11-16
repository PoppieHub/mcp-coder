---
name: mcp-coder-delegator
description: Orchestrates mcp-coder to delegate well-scoped coding, testing, refactoring, and documentation tasks to a separate coding model while the primary agent plans and reviews. Use when mcp-coder tools are available and the user asks to implement, fix, refactor, test, document, or parallelize repository work.
---

# MCP Coder Delegator

Use `mcp-coder` as an implementation worker, not as the owner of the task. Keep product intent, architecture, ambiguous choices, destructive actions, and final acceptance with the primary agent.

Tool names may be namespaced by the host. Match them by their final name, such as `execute_coding_task`, `describe_project`, or `wait_task`.

## Prerequisite

This skill requires the `mcp-coder` MCP server and its tools. If they are unavailable, say that the server must be installed and configured; do not imitate tool calls or claim delegation occurred. Read [references/setup.md](references/setup.md) only when installation, host configuration, or connection diagnostics are needed.

## Decide what to delegate

Delegate a task when its desired outcome is concrete and its write scope can be bounded. Good candidates include implementing an already-decided change, fixing a localized bug, adding tests, performing a mechanical refactor, and updating documentation.

Do not delegate unresolved architecture, requirements discovery, security-sensitive judgment, release decisions, credential handling, or final review. Resolve uncertainty first or keep that work with the primary agent.

Combine edits that must stay consistent into one task. Split only independent work whose allowed paths do not overlap.

## Build the task contract

Before calling the worker:

1. Inspect only enough context to identify the outcome, relevant files, constraints, and acceptance criteria. Preserve repository instructions such as `AGENTS.md`, `CLAUDE.md`, or `CONTRIBUTING.md`.
2. Call `describe_project` with the workspace root and likely files when the stack or verification command is not already known. Prefer this over manually rediscovering manifests.
3. Pass a complete contract:
   - `task`: the concrete outcome and observable acceptance criteria;
   - `workspaceRoot`: the repository root;
   - `suggestedFiles`: the smallest useful starting set;
   - `allowedChangePaths`: the narrowest paths that can contain the solution;
   - `constraints`: compatibility, behavior, style, and explicit non-goals;
   - `projectContext`: facts already established, without speculation;
   - `verification` and `verificationDir`: one relevant repository-declared or standard toolchain check when known;
   - `requireChanges`: `true` for implementation tasks;
   - `requireVerification`: `true` for code and tests.

Do not send vague instructions such as “fix it,” “improve the code,” or “explore the repo.” Do not delegate the same investigation the primary agent has already performed.

For documentation-only changes or exact mechanical replacements that genuinely need no executable check, set `requireVerification` to `false` explicitly. Do not disable verification merely to avoid a failing check.

Read [references/recipes.md](references/recipes.md) when a concrete contract template would save time. Read [references/playbook.md](references/playbook.md) for task partitioning and recovery decisions.

## Execute

Use `execute_coding_task` for one task.

For two or more independent tasks:

1. Confirm their `allowedChangePaths` are disjoint and preferably belong to different modules or packages.
2. Start each with `start_coding_task`.
3. Collect every task with `wait_task`; if a task is still running, report useful progress and wait again.
4. Never parallelize tasks that edit a shared file, generated artifact, lockfile, schema, or cross-cutting interface.

Use `ask_coder` only for a focused read-only question when attaching specific files will save context. The primary agent remains responsible for interpreting the answer.

## Review the result

Treat worker output as a candidate change, not proof of completion.

1. Check `status`, `changedFiles`, `diff`, `verification`, `warnings`, and `failedVerification`.
2. Inspect the diff against the user's request, repository rules, allowed scope, and unintended behavior.
3. Confirm every affected module has an appropriate successful check. Run or request any missing high-value validation.
4. If the result is `needs_clarification`, answer the underlying question yourself when possible, then send a narrower follow-up task.
5. If the result is `failed`, use the concrete failure to make one targeted retry. Do not repeat an unchanged task or loop blindly. Read [references/troubleshooting.md](references/troubleshooting.md) when the failure is operational or its next action is unclear.
6. Report completion only after the primary agent accepts the diff and verification evidence.

## User-facing handoff

Summarize the outcome, important files changed, and verification performed. Mention warnings or unresolved risks plainly. Do not expose internal prompts, token-saving estimates, or raw worker transcripts unless the user asks.
