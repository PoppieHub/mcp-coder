# Troubleshooting

Use this reference for operational failures and ambiguous worker outcomes. Never expose tokens in prompts, logs, screenshots, or task fields.

| Symptom | Likely cause | Next action |
| --- | --- | --- |
| No `mcp-coder` tools appear | Host has not loaded the stdio server or needs a restart | Check the absolute command path and environment in [setup.md](setup.md), restart the host, then run diagnostics. |
| `doctor` reports missing configuration | One of the three required model variables is absent | Set `MCP_LLM_TOKEN`, `MCP_LLM_BASE_URL`, and `MCP_LLM_MODEL` in the server environment. |
| Endpoint returns 401 or 403 | Invalid token, incompatible endpoint, or provider policy | Verify the endpoint outside the skill without revealing the token; use `doctor --api` only with authorization. |
| Endpoint returns 404 | Base URL already contains `/v1/messages` or is not Anthropic Messages-compatible | Set the base URL to the provider API root expected by mcp-coder. |
| Task is rejected before starting | Invalid workspace, overlapping task scopes, or the parallel-task limit is reached | Inspect the error, wait for running tasks, and make `allowedChangePaths` disjoint. |
| Worker requests clarification | The contract leaves a behavior or product decision unresolved | Make the decision with the primary agent, then submit one narrower contract. |
| Worker changes nothing | The requested outcome may already hold or the scope excludes the solution | Inspect the repository and result; do not claim success solely from the worker summary. |
| Verification is refused | Command is not repository-declared, not a standard toolchain command, or references an outside path | Use `describe_project` and select a supported check inside the workspace. |
| Verification fails | The implementation is incomplete or the selected check exposes an existing issue | Inspect `failedVerification`; make one targeted retry only when the failure adds concrete information. |
| A warning is returned with completed status | The result has unresolved risk despite changed files | Resolve the warning or report it; do not silently mark the task done. |
| Large file context is truncated | The attached file exceeds the configured read window | Narrow the task and suggested files or let the worker read the relevant window itself. |
| Repeated identical failure | The task is unsuitable for further delegation | Stop retrying and handle it with the primary agent or ask for missing input. |

## Safe diagnostic sequence

1. Run the local `doctor` command without `--api`.
2. Confirm the host exposes `get_settings` or `run_diagnostics`.
3. Use `describe_project` on the intended workspace and target files.
4. Test one small task with narrow write paths in a clean workspace.
5. Inspect the returned diff and verification evidence.

Do not widen file scope, disable verification, or increase retry count merely to make a failing task appear successful.
