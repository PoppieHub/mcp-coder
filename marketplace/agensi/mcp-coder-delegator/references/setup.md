# Setup and Connection Guide

Use this reference only when the host cannot see the `mcp-coder` tools or the buyer asks how to install them. Keep tokens and credentials out of chat prompts and task contracts.

## 1. Install the matching release

Download the archive for the buyer's OS and architecture from:

https://github.com/PoppieHub/mcp-coder/releases/tag/v0.1.0

Extract the executable to a stable absolute path. On macOS or Linux, make it executable if necessary:

```bash
chmod +x /absolute/path/mcp-coder
```

The source repository and complete server documentation are at:

https://github.com/PoppieHub/mcp-coder

## 2. Configure the worker model

The server needs an Anthropic Messages-compatible endpoint:

- `MCP_LLM_TOKEN`
- `MCP_LLM_BASE_URL`
- `MCP_LLM_MODEL`

Validate local configuration without sending a model request:

```bash
MCP_LLM_TOKEN='...' \
MCP_LLM_BASE_URL='https://llm.example.com/api' \
MCP_LLM_MODEL='coder-medium' \
/absolute/path/mcp-coder doctor
```

Run `doctor --api` only when the buyer authorizes a provider request that may incur usage.

## 3. Connect a host

### Claude Code

```bash
claude mcp add mcp-coder \
  --env MCP_LLM_TOKEN \
  --env MCP_LLM_BASE_URL \
  --env MCP_LLM_MODEL \
  -- /absolute/path/mcp-coder
```

Set the three variables in the environment used to start Claude Code. Do not reuse or overwrite `ANTHROPIC_*` variables; `mcp-coder` is a separate model connection.

### Codex

Add this server entry to the Codex configuration and replace the example values:

```toml
[mcp_servers.mcp-coder]
command = "/absolute/path/mcp-coder"

[mcp_servers.mcp-coder.env]
MCP_LLM_TOKEN = "..."
MCP_LLM_BASE_URL = "https://llm.example.com/api"
MCP_LLM_MODEL = "coder-medium"
```

### Other stdio MCP hosts

Use the host's MCP server UI or configuration file with the same command and environment values:

```json
{
  "mcpServers": {
    "mcp-coder": {
      "command": "/absolute/path/mcp-coder",
      "env": {
        "MCP_LLM_TOKEN": "...",
        "MCP_LLM_BASE_URL": "https://llm.example.com/api",
        "MCP_LLM_MODEL": "coder-medium"
      }
    }
  }
}
```

The exact settings location varies by host. Preserve the command and environment semantics rather than guessing a host-specific file path.

## 4. Verify the connection

After restarting the host, confirm that it exposes these tools:

- `execute_coding_task`
- `start_coding_task` and `wait_task`
- `describe_project`
- `ask_coder`
- `get_settings` or `run_diagnostics`

Use `run_diagnostics` or the local `doctor` command before testing against a real repository. Then run one small bounded task in a disposable or clean workspace and review its diff.

If a step fails, continue with [troubleshooting.md](troubleshooting.md).
