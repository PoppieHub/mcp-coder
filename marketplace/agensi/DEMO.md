# 30-second demo: MCP Coder Delegator

## Goal

Show the paid orchestration workflow, not a generic code-generation montage. The viewer should understand the difference between the free executor and the premium skill within the first 10 seconds.

## Recording setup

- 1920×1080 or 1440×900, 30 fps.
- Large editor/terminal font and no visible secrets, tokens, personal paths, or unrelated notifications.
- Use a disposable repository or a clean demo branch.
- Keep the Agensi listing link visible only in the final frame.
- Target 28–32 seconds; do not accelerate text until it becomes unreadable.

## Shot list

| Time | Visual | On-screen caption |
| --- | --- | --- |
| 0:00–0:03 | Show the request in the primary coding agent. | `One request. Bounded delegation.` |
| 0:03–0:08 | The skill converts it into two contracts with explicit files, constraints, and verification. | `Scope before execution` |
| 0:08–0:13 | Highlight the disjoint `allowedChangePaths` for backend and frontend tasks. | `No overlapping writes` |
| 0:13–0:18 | Start both tasks before waiting. Show two task IDs. | `Independent work runs in parallel` |
| 0:18–0:24 | Show compact results: changed files, checks passed, and returned diffs. | `Verified results, not promises` |
| 0:24–0:28 | Primary agent reviews the diffs and accepts the result. | `Final control stays with your agent` |
| 0:28–0:31 | Show the workflow graphic and CTA. | `MCP Coder Delegator · $12 on Agensi` |

## Demo request

```text
Implement the approved backend validation and the independent frontend empty
state. Keep their write scopes disjoint, run both tasks in parallel, verify each
module, and review both returned diffs before reporting success.
```

## Voiceover

> MCP Coder Delegator turns one coding request into bounded worker contracts. It keeps write scopes disjoint, runs independent tasks in parallel, requires verification, and returns every diff to the primary agent for final review. Delegate implementation. Keep control of the code.

## Captions

```srt
1
00:00:00,000 --> 00:00:03,000
One request. Bounded delegation.

2
00:00:03,000 --> 00:00:08,000
Scope before execution.

3
00:00:08,000 --> 00:00:13,000
No overlapping writes.

4
00:00:13,000 --> 00:00:18,000
Independent work runs in parallel.

5
00:00:18,000 --> 00:00:24,000
Verified results, not promises.

6
00:00:24,000 --> 00:00:28,000
Final control stays with your agent.

7
00:00:28,000 --> 00:00:31,000
MCP Coder Delegator · $12 on Agensi
```

## Publishing copy

**Title:** Delegate coding work without giving up control

**Description:** MCP Coder Delegator turns implementation requests into bounded mcp-coder contracts, parallelizes only disjoint edits, requires verification, and keeps final diff review with the primary coding agent.

**CTA:** https://www.agensi.io/r/POPPIEHUB?to=/skills/mcp-coder-delegator&utm_source=demo

## Verified demo run

The storyboard is backed by a real two-task run against a disposable repository:

- both tasks were started before either result was collected;
- write scopes were disjoint: `backend/validate.js` and `frontend/empty-state.js`;
- the executor changed only those two files and returned both diffs;
- worker verification passed independently in both Node modules;
- primary-agent verification was repeated after completion: 3 of 3 tests passed;
- no warnings or out-of-scope changes were reported.

Render the 31-second video from the checked-in artwork:

```sh
scripts/render-agensi-demo.sh
```
