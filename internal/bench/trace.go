package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
)

// Long trace text is truncated: the step sequence matters, not the complete output.
const traceClip = 600

// tracingClient writes every model exchange to JSONL: what tools returned on the previous step
// and how the model responded. This reveals where a task became stuck.
type tracingClient struct {
	inner llm.Client
	mu    sync.Mutex
	f     *os.File
	step  int
}

type traceLine struct {
	Step    int              `json:"step"`
	Results []map[string]any `json:"results,omitempty"`
	Reply   []map[string]any `json:"reply,omitempty"`
	Stop    string           `json:"stop,omitempty"`
	Usage   *llm.Usage       `json:"usage,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func (t *tracingClient) Message(ctx context.Context, r llm.Request) (llm.Response, error) {
	out, err := t.inner.Message(ctx, r)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.step++
	line := traceLine{Step: t.step}
	if n := len(r.Messages); n > 0 {
		switch c := r.Messages[n-1].Content.(type) {
		case string:
			line.Results = []map[string]any{{"text": clip(c)}}
		case []map[string]any:
			for _, b := range c {
				entry := map[string]any{}
				for _, k := range []string{"type", "is_error"} {
					if v, ok := b[k]; ok {
						entry[k] = v
					}
				}
				for _, k := range []string{"content", "text"} {
					if v, ok := b[k].(string); ok {
						entry[k] = clip(v)
					}
				}
				line.Results = append(line.Results, entry)
			}
		}
	}
	for _, b := range out.Content {
		entry := map[string]any{"type": b.Type}
		if b.Text != "" {
			entry["text"] = clip(b.Text)
		}
		if b.Name != "" {
			entry["name"] = b.Name
			entry["input"] = clip(string(b.Input))
		}
		line.Reply = append(line.Reply, entry)
	}
	line.Stop, line.Usage = out.StopReason, out.Usage
	if err != nil {
		line.Error = err.Error()
	}
	if raw, e := json.Marshal(line); e == nil {
		_, _ = t.f.Write(append(raw, '\n'))
	}
	return out, err
}

func clip(s string) string {
	if len(s) > traceClip {
		return s[:traceClip] + fmt.Sprintf("…[+%d]", len(s)-traceClip)
	}
	return s
}

// clientFor returns the task client: the Runner-provided client or HTTP, wrapped with tracing when TraceDir is set.
func (b Runner) clientFor(id string, run int) (func(config.Config) llm.Client, func()) {
	base := b.NewClient
	if base == nil {
		base = func(c config.Config) llm.Client {
			return &llm.HTTPClient{C: c, HTTP: &http.Client{Timeout: c.RequestTimeout}}
		}
	}
	if b.TraceDir == "" {
		return base, func() {}
	}
	if err := os.MkdirAll(b.TraceDir, 0755); err != nil {
		return base, func() {}
	}
	f, err := os.Create(fmt.Sprintf("%s/%s-%d.jsonl", b.TraceDir, id, run))
	if err != nil {
		return base, func() {}
	}
	return func(c config.Config) llm.Client { return &tracingClient{inner: base(c), f: f} }, func() { _ = f.Close() }
}
