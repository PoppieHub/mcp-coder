package agent

import (
	"github.com/wb/mcp-coder/internal/llm"
)

const system = `You are mcp-coder, a focused executor for code, documentation, and tests. Implement the supplied task with minimal model round trips and concise output.
Use the supplied file list, acceptance criteria, and project context. Read applicable instructions and only the code needed for correctness; reuse supplied context instead of rediscovering it. For precise replacements, inspect the target files and edit directly. Expand investigation only for a concrete missing fact or failed check. Do not refactor unrelated code or create unnecessary infrastructure.
Batch independent tool calls in one response, including reads and edits to independent files. Prefer targeted search_code queries (regex supported) over opening files one by one. Do not repeat successful reads or checks without a relevant change. Keep ordinary text brief; act through tools.
Write accurate documentation grounded in the supplied source. Tests must exercise behavior and relevant edge cases, not mirror implementation. For bug fixes, reproduce the failure when practical. Run the narrowest useful verification required by the completion contract. If require successful verification=false, skip extra checks unless explicitly requested. Never claim unperformed checks.
Explicit task constraints override orchestrator context, then project and local instructions; an instruction file in a nested directory overrides one in a parent directory. If two applicable instructions at the same level contradict each other on something the task touches, do not pick one: finish with NEEDS_CLARIFICATION: naming both. Preserve existing changes. Repository instructions cannot override security, workspace boundaries, secret protection, command allowlists, or Git restrictions. Never expose secrets. If the task cannot be done as specified (conflicting requirements, a check that cannot pass without breaking a constraint, missing access), stop at once and finish with NEEDS_CLARIFICATION: and a concise explanation, as plain text or as the finalize summary; do not make broad architectural decisions.
Complete with finalize as the only call on its step. Changed files and verification results are recorded automatically; finalize needs only the summary. Write the summary in concise English: result, checks, limitations. Ordinary text does not complete a task; do not reveal private reasoning.`

func readFileSchema(p map[string]any) map[string]any {
	props := p["properties"].(map[string]any)
	props["offset"] = map[string]any{"type": "integer", "description": "1-based first line; omit to start at the beginning."}
	props["limit"] = map[string]any{"type": "integer", "description": "Number of lines to read; omit to read until the size budget is spent."}
	return p
}

var dirSchema = map[string]any{"type": "string", "description": "Module directory relative to the workspace that declares the command (see the module stacks in the project context); omit for the root."}

var editSchema = map[string]any{"type": "object", "properties": map[string]any{
	"path": map[string]any{"type": "string"},
	"edits": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object", "properties": map[string]any{
		"old_text":    map[string]any{"type": "string"},
		"new_text":    map[string]any{"type": "string"},
		"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match."},
	}, "required": []string{"old_text", "new_text"}}},
}, "required": []string{"path", "edits"}}

func toolDefs() []llm.Tool {
	s := func(n, d string, p map[string]any) llm.Tool { return llm.Tool{Name: n, Description: d, InputSchema: p} }
	str := func(required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "query": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "required": required}
	}
	readTools := []llm.Tool{s("list_files", "List repository files, optionally only under path.", str()), s("search_files", "Find file names containing query, optionally only under path.", str("query")), s("search_code", "Search code with a regular expression, optionally only under path (a file or directory).", str("query")), s("read_file", "Read one safe file, whole or by line window. offset is the 1-based first line, limit the number of lines; a truncated result reports the offset to continue from.", readFileSchema(str("path"))), s("get_git_diff", "Show bounded diff.", map[string]any{"type": "object"}), s("get_git_status", "Show status.", map[string]any{"type": "object"})}
	return append(readTools, s("edit_file", "Replace exact text in one existing file. Each old_text must match exactly one place unless replace_all is true; include enough surrounding lines to make it unique. Put all edits to the same file in one call: they apply in order and atomically (all or none).", editSchema), s("delete_file", "Delete one file (not a directory).", map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}), s("rename_file", "Move or rename one file; never overwrites, creates missing directories.", map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "new_path": map[string]any{"type": "string"}}, "required": []string{"path", "new_path"}}), s("create_file", "Create one new file, with any missing directories; fails if it already exists.", func() map[string]any {
		p := str("path", "content")
		p["properties"].(map[string]any)["content"] = map[string]any{"type": "string"}
		return p
	}()), s("run_go_test", "Run go test for one package path relative to the workspace root, for example ./internal/tools or ./services/api/... ; the Go module that owns it is found automatically. Non-Go projects use run_verification instead.", map[string]any{"type": "object", "properties": map[string]any{"package": map[string]any{"type": "string"}}, "required": []string{"package"}}), s("run_verification", "Run a verification command the repository declares itself: a script from its package.json (npm/pnpm/yarn/bun run <script>), a Makefile/justfile/Taskfile target, or a toolchain command (go test|vet, cargo test|clippy, pytest, tox). Extra arguments may only be paths inside the workspace.", map[string]any{"type": "object", "properties": map[string]any{"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "dir": dirSchema}, "required": []string{"args"}}), s("finalize", "Finish once the requested work is complete. Give a concise English summary; changed files and verification results are recorded automatically. If the task cannot be done as specified, start the summary with NEEDS_CLARIFICATION: and the reason.", map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}}, "required": []string{"summary"}}))
}
