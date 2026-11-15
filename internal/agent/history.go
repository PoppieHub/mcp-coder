package agent

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/wb/mcp-coder/internal/llm"
)

// Old tool results are compacted only after the budget is exceeded, with headroom:
// every compaction changes the conversation prefix and invalidates the prompt cache, so doing
// it occasionally in larger chunks is cheaper than doing it on every step. The first message
// (the task) and the most recent turns are preserved.
const (
	compactKeepRecent = 4
	compactMinChars   = 800
)

func compactHistory(msgs []llm.Message, budget int) []llm.Message {
	if budget <= 0 || historySize(msgs) <= budget {
		return msgs
	}
	target := budget * 3 / 5
	for i := 1; i < len(msgs)-compactKeepRecent && historySize(msgs) > target; i++ {
		switch content := msgs[i].Content.(type) {
		case []map[string]any:
			for _, block := range content {
				if text, ok := block["content"].(string); ok && block["type"] == "tool_result" && len(text) > compactMinChars {
					block["content"] = fmt.Sprintf("[elided to save context: %d chars of earlier tool output; rerun the tool if it is still needed]", len(text))
				}
			}
		case []llm.Block:
			for j, block := range content {
				if block.Type == "tool_use" && len(block.Input) > compactMinChars {
					content[j].Input = elidedInput(block.Input)
				}
			}
		}
	}
	return msgs
}

// elidedInput preserves the path from a large tool input so the history remains understandable.
func elidedInput(raw json.RawMessage) json.RawMessage {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	out := map[string]any{"elided": fmt.Sprintf("%d chars of input", len(raw))}
	if path, ok := in["path"].(string); ok {
		out["path"] = path
	}
	b, _ := json.Marshal(out)
	return b
}

func historySize(msgs []llm.Message) int {
	b, _ := json.Marshal(msgs)
	return len(b)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "…" + s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func errText(s string, e error) string {
	if e != nil {
		return errPrefix + " " + e.Error() + "\n" + s
	}
	return s
}

func addUsage(a, b llm.Usage) llm.Usage {
	a.LLMRequests += b.LLMRequests
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	a.Available = a.Available || b.Available
	a.Partial = a.Partial || b.Partial
	return a
}

func mapKeys(m map[string]bool) []string {
	o := make([]string, 0, len(m))
	for k := range m {
		o = append(o, k)
	}
	return o
}
