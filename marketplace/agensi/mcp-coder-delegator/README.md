# MCP Coder Delegator

A premium orchestration skill for [`mcp-coder`](https://github.com/PoppieHub/mcp-coder). It teaches a primary coding agent to turn implementation requests into bounded worker contracts, parallelize only disjoint edits, and review diffs and verification before reporting success.

## Requirements

- `mcp-coder` installed and exposed to the host agent as an MCP server
- an Anthropic Messages-compatible model endpoint configured for `mcp-coder`
- a coding agent that can load `SKILL.md` skills

Install the matching [`mcp-coder` v0.1.0 release](https://github.com/PoppieHub/mcp-coder/releases/tag/v0.1.0). Source and additional setup instructions are available in the [repository](https://github.com/PoppieHub/mcp-coder).

## Use

Ask the agent to implement, fix, refactor, test, or document code with mcp-coder. The skill activates automatically when the server tools are available. It does not install the server, supply a model, or make architecture and product decisions on the user's behalf.

Example request:

> Use mcp-coder to fix the retry race, add a regression test, and verify the affected package.

The primary agent should scope the task, delegate implementation, inspect the returned diff, and report the checks it accepted.
