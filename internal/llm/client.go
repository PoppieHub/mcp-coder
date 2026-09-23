package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/wb/mcp-coder/internal/config"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client interface {
	Message(context.Context, Request) (Response, error)
}
type HTTPClient struct {
	C    config.Config
	HTTP *http.Client
}
type Request struct {
	System    string    `json:"system"`
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
}
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
type Response struct {
	Content    []Block `json:"content"`
	StopReason string  `json:"stop_reason"`
	Usage      *Usage  `json:"usage,omitempty"`
}
type Usage struct {
	Available    bool `json:"available"`
	Partial      bool `json:"partial,omitempty"`
	InputTokens  int  `json:"input_tokens,omitempty"`
	OutputTokens int  `json:"output_tokens,omitempty"`
	LLMRequests  int  `json:"llmRequests,omitempty"`
}

func (u Usage) TotalTokens() int { return u.InputTokens + u.OutputTokens }

type Block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

func (c *HTTPClient) Message(ctx context.Context, r Request) (Response, error) {
	r.Model = c.C.Model
	b, e := json.Marshal(r)
	if e != nil {
		return Response{}, e
	}
	var last error
	for i := 0; i < 3; i++ {
		qctx, cancel := context.WithTimeout(ctx, c.C.RequestTimeout)
		defer cancel()
		req, e := http.NewRequestWithContext(qctx, "POST", c.C.BaseURL+"/v1/messages", bytes.NewReader(b))
		if e != nil {
			cancel()
			last = e
			continue
		}
		if e == nil {
			req.Header.Set("Authorization", "Bearer "+c.C.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("anthropic-version", "2023-06-01")
			res, e := c.HTTP.Do(req)
			if e == nil {
				body, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
				closeErr := res.Body.Close()
				if readErr != nil {
					e = fmt.Errorf("не удалось прочитать ответ LLM API: %w", readErr)
				} else if closeErr != nil {
					e = fmt.Errorf("не удалось закрыть ответ LLM API: %w", closeErr)
				}
				if e != nil {
					last = e
					continue
				}
				if res.StatusCode >= 200 && res.StatusCode < 300 {
					var out Response
					e = json.Unmarshal(body, &out)
					cancel()
					if e != nil {
						return Response{}, fmt.Errorf("invalid API response: %w", e)
					}
					return out, nil
				}
				e = fmt.Errorf("LLM API вернул HTTP %d: %s", res.StatusCode, trunc(string(body), 400))
				if res.StatusCode == 401 || res.StatusCode == 403 {
					cancel()
					return Response{}, e
				}
				if res.StatusCode < 500 && res.StatusCode != 429 {
					cancel()
					return Response{}, e
				}
			}
			cancel()
		}
		last = e
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if i < 2 {
			timer := time.NewTimer(time.Duration(i+1) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Response{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return Response{}, last
}
func trunc(s string, n int) string {
	if len(s) > n {
		return strings.TrimSpace(s[:n]) + "…"
	}
	return strings.TrimSpace(s)
}
