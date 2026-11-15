# Agensi listing: MCP Coder Delegator

## Listing fields

- **Name:** MCP Coder Delegator
- **Slug:** `mcp-coder-delegator`
- **Tagline:** Delegate the implementation. Keep control of the code.
- **Price:** $12 one-time
- **Category:** Agents & Orchestration
- **Tags:** `mcp`, `coding-agent`, `codex`, `claude-code`, `cursor`, `delegation`, `testing`, `developer-tools`
- **Logo:** `assets/mcp-coder-delegator-logo.png`
- **Creator avatar:** `assets/poppiehub-avatar.png`

## Short description

Turn coding requests into tightly scoped mcp-coder tasks, run independent work in parallel, and review verified diffs before anything is declared done.

## Full description

### Stop spending your primary agent's context on implementation churn

Your best coding agent should be making decisions—not repeatedly reading the same files, applying mechanical edits, rerunning tests, and carrying every intermediate tool result in its context.

MCP Coder Delegator gives Claude Code, Codex, Cursor, Copilot, and other skill-aware agents a disciplined workflow for handing implementation work to the open-source `mcp-coder` server while keeping planning, judgment, and final review with the primary agent.

Instead of sending a vague “fix this” prompt to a worker model, the skill builds a precise execution contract: the required outcome, relevant files, allowed write scope, known project facts, constraints, acceptance criteria, and the exact verification to run.

The result is controlled delegation you can actually review.

### What it helps your agent do

- **Delegate with boundaries** — restrict every task to the files or directories it is allowed to change.
- **Avoid repeated exploration** — pass known repository facts to the worker instead of paying another model to rediscover them.
- **Choose meaningful checks** — use the project stack to select targeted tests, builds, or validation commands.
- **Parallelize safely** — run independent backend, frontend, package, or documentation tasks concurrently without overlapping write scopes.
- **Catch false completion** — require real file changes and successful verification before accepting a coding task as complete.
- **Recover intelligently** — turn failures and clarification requests into targeted follow-up tasks instead of blind retries.
- **Keep the primary agent in control** — review the returned diff, warnings, and verification evidence before reporting success.

### From vague request to verifiable result

Without this skill:

> Fix the retry bug and add tests.

With MCP Coder Delegator, the agent turns that request into a bounded worker contract:

- change only the retry implementation and its tests;
- preserve the exported API and cancellation behavior;
- reuse the existing fake clock;
- add a regression test for the duplicate retry;
- run the affected package's test suite;
- return the diff and verification result for primary-agent review.

That difference is where reliable delegation begins.

### Included

- production-ready `SKILL.md` orchestration workflow;
- decision rules for what should and should not be delegated;
- contract patterns for bug fixes, tests, refactors, and documentation;
- safe parallel-task partitioning rules;
- failure and clarification recovery matrix;
- final-review checklist for diffs, warnings, and verification;
- setup and credential-safety guidance.

### Works with

- Claude Code
- OpenAI Codex
- Cursor
- GitHub Copilot
- other agents that support `SKILL.md` and can connect to MCP servers

### Requirements

This skill is the orchestration layer for the free, open-source `mcp-coder` server. The server must already be installed and connected to the host agent, with access to an Anthropic Messages-compatible model endpoint.

The purchase does not include API credits, a hosted model, credentials, or the server binary.

### Built for developers who want leverage without giving up control

Use a cheaper or specialized model for bounded implementation work. Keep your primary agent focused on architecture, product intent, difficult decisions, and final acceptance.

Delegate the implementation. Keep control of the code.

## Search preview

**MCP Coder Delegator — Delegate the implementation. Keep control of the code.**

Orchestrate mcp-coder from Claude Code, Codex, Cursor, and Copilot with bounded file scopes, safe parallel tasks, required verification, and primary-agent diff review.

## Suggested launch assets

### Demo title

**One prompt, two coding agents, zero overlapping edits**

### 30-second demo flow

1. Ask the primary agent to fix a backend bug and add an independent frontend state.
2. Show it creating two disjoint mcp-coder contracts.
3. Show both tasks running in parallel.
4. Show the returned diffs and passing checks.
5. End on: **Delegate the implementation. Keep control of the code.**

### Social launch copy

I built a skill that teaches Claude Code and Codex how to delegate implementation work without handing a worker model the keys to the whole repository.

MCP Coder Delegator creates bounded tasks, selects verification, runs independent edits in parallel, and makes the primary agent review the diff before declaring success.

Built for the open-source mcp-coder MCP server.

## Pricing recommendation

Launch at **$12 one-time**. Move to **$19** after the listing has reviews, a polished demo, and at least one documented workflow showing a real before-and-after result. Avoid a subscription until updates provide recurring value.
