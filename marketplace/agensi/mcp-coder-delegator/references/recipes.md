# Ready-to-Adapt Task Contracts

Use the smallest recipe that matches the requested outcome. Replace placeholders with facts already established from the repository; do not copy examples blindly.

## Localized bug fix

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
  scheduleRetry is called from send; existing tests use a fake clock.
verification: go test ./internal/client
verificationDir: .
requireChanges: true
requireVerification: true
```

## Tests for existing behavior

```yaml
task: >-
  Add table-driven tests for parseLimit covering the documented default,
  minimum, maximum, malformed input, and out-of-range values. Do not change
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

The production file is useful reading context but is intentionally excluded from the write scope.

## Mechanical refactor

```yaml
task: >-
  Replace repeated inline timeout parsing with the existing parseTimeout helper.
  Preserve all exported behavior and error messages.
suggestedFiles:
  - internal/api/handler.go
  - internal/api/handler_test.go
allowedChangePaths:
  - internal/api/handler.go
  - internal/api/handler_test.go
constraints:
  - Do not introduce a new abstraction.
  - Do not modify public response types.
verification: go test ./internal/api
requireChanges: true
requireVerification: true
```

## Documentation-only update

```yaml
task: >-
  Document REQUEST_TIMEOUT_MS with its default, valid range, and one shell
  example. Keep the existing tone and table layout.
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

## Independent parallel tasks

Start both contracts before waiting for either result.

Task A:

```yaml
task: Add validation and regression coverage for the backend create endpoint.
suggestedFiles: [api/create.go, api/create_test.go]
allowedChangePaths: [api/create.go, api/create_test.go]
verification: go test ./api
requireChanges: true
requireVerification: true
```

Task B:

```yaml
task: Add the already-approved empty state to the project list component.
suggestedFiles: [web/src/ProjectList.tsx, web/src/ProjectList.test.tsx]
allowedChangePaths: [web/src/ProjectList.tsx, web/src/ProjectList.test.tsx]
verification: npm test -- ProjectList.test.tsx
verificationDir: web
requireChanges: true
requireVerification: true
```

These scopes are disjoint. Do not use this pattern when both tasks edit a schema, shared type, lockfile, generated artifact, or common interface.

## Targeted retry after failed verification

Do not resend the original contract unchanged. Carry forward the existing diff and the concrete failure:

```yaml
task: >-
  Correct the existing retry implementation so TestCancelStopsPendingRetry
  passes. Keep the current regression test for duplicate scheduling.
suggestedFiles:
  - internal/client/retry.go
  - internal/client/retry_test.go
allowedChangePaths:
  - internal/client/retry.go
  - internal/client/retry_test.go
constraints:
  - Preserve cancellation semantics.
  - Do not rewrite unrelated retry tests.
projectContext: >-
  The previous diff fixed duplicate scheduling but failed
  TestCancelStopsPendingRetry because the timer remains armed after cancel.
verification: go test ./internal/client
requireChanges: true
requireVerification: true
```

Retry once with new information. If the same failure repeats, stop delegating and resolve it with the primary agent.
