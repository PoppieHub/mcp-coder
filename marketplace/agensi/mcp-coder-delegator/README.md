# MCP Coder Delegator

An orchestration skill for the open-source `mcp-coder` MCP server. It teaches a primary coding agent to turn implementation requests into bounded worker contracts, parallelize only disjoint edits, and review diffs and verification before reporting success.

## Requirements

- `mcp-coder` installed and exposed to the host agent as an MCP server
- an Anthropic Messages-compatible model endpoint configured for `mcp-coder`
- a coding agent that can load `SKILL.md` skills

The server and setup instructions are available at `https://github.com/PoppieHub/mcp-coder`. Prebuilt archives are published at `https://github.com/PoppieHub/mcp-coder/releases`.

## Use

Ask the agent to implement, fix, refactor, test, or document code with mcp-coder. The skill activates automatically when the server tools are available. It does not install the server, supply a model, or make architecture and product decisions on the user's behalf.

Example request:

> Use mcp-coder to fix the retry race, add a regression test, and verify the affected package.

The primary agent should scope the task, delegate implementation, inspect the returned diff, and report the checks it accepted.
