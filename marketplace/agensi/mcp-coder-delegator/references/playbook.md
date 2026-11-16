# Delegation Playbook

Use this reference when deciding how to partition work, run it in parallel, or recover after a worker does not complete cleanly. For ready-to-adapt task contracts, read [recipes.md](recipes.md).

## Parallel partitioning

Parallel work is safe when tasks share intent but not writable state. Good partitions include:

- a backend package and an independent frontend package;
- separate language modules with separate checks;
- documentation in one file and isolated implementation in another, provided neither task regenerates shared output.

Keep work sequential when tasks touch any of the following:

- the same file or overlapping directory scopes;
- a shared interface, schema, migration chain, lockfile, snapshot, or generated file;
- behavior where the second task depends on the exact diff produced by the first;
- checks that mutate shared state or cannot safely run concurrently.

Start every safe task before waiting for the first. Waiting immediately after each start makes the work sequential.

## Recovery matrix

| Result | Primary-agent action |
| --- | --- |
| `completed`, clean diff, successful check | Review behavior and accept or perform proportional final validation. |
| `completed`, warning present | Resolve the warning before claiming success. |
| `needs_clarification` | Make the missing decision, narrow the contract, and resubmit once. |
| `failed`, verification failure | Inspect `failedVerification`; retry only with a concrete correction or fix locally. |
| `failed`, scope violation | Tighten paths and task wording; widen scope only when the solution actually requires it. |
| Partial changes after cancellation | Inspect the retained diff before continuing or assigning overlapping work. |
| Repeated unchanged failure | Stop delegating that task and handle it with the primary agent or request genuinely missing input. |

## Final acceptance checklist

- Every changed file belongs to the declared write scope.
- The diff solves the requested behavior rather than only satisfying a test.
- Public APIs and stated invariants remain intact.
- Each affected module has a relevant successful check.
- No warning, clarification request, or failed verification is being hidden.
- The primary agent, not the worker summary, made the final acceptance decision.

Stop after one unchanged failure or one failed targeted retry. Repeated retries spend context without adding information.
