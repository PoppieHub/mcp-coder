package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/wb/mcp-coder/internal/config"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Client interface {
	Message(context.Context, Request) (Response, error)
}
type HTTPClient struct {
	C    config.Config
	HTTP *http.Client
	// noCache is set when the gateway rejects cache_control; subsequent requests omit the markers.
	noCache atomic.Bool
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
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
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
	// Tokens read from and written to the prompt cache; they are not included in InputTokens.
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_creation_input_tokens,omitempty"`
	LLMRequests      int `json:"llmRequests,omitempty"`
}

func (u Usage) TotalTokens() int { return u.InputTokens + u.OutputTokens }

type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	CacheControl map[string]any  `json:"cache_control,omitempty"`
}

var ephemeral = map[string]any{"type": "ephemeral"}

// wireRequest is the request body; with caching, system becomes an array of marked blocks.
type wireRequest struct {
	System    any       `json:"system"`
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
}

// body serializes a request. With caching, markers are placed on system, the last tool, and the
// last message: on every agent step, the prefix through the previous marker matches and is read
// from cache. Messages are copied so markers do not accumulate in caller history (the limit is four).
func body(r Request, cache bool) ([]byte, error) {
	w := wireRequest{System: r.System, Model: r.Model, MaxTokens: r.MaxTokens, Messages: r.Messages, Tools: r.Tools}
	if cache {
		w.System = []map[string]any{{"type": "text", "text": r.System, "cache_control": ephemeral}}
		if n := len(r.Tools); n > 0 {
			w.Tools = append([]Tool(nil), r.Tools...)
			w.Tools[n-1].CacheControl = ephemeral
		}
		if n := len(r.Messages); n > 0 {
			w.Messages = append([]Message(nil), r.Messages...)
			w.Messages[n-1].Content = withCacheMark(w.Messages[n-1].Content)
		}
	}
	return json.Marshal(w)
}

func withCacheMark(content any) any {
	switch v := content.(type) {
	case string:
		return []map[string]any{{"type": "text", "text": v, "cache_control": ephemeral}}
	case []map[string]any:
		if len(v) == 0 {
			return v
		}
		out := append([]map[string]any(nil), v...)
		last := make(map[string]any, len(out[len(out)-1])+1)
		for k, val := range out[len(out)-1] {
			last[k] = val
		}
		last["cache_control"] = ephemeral
		out[len(out)-1] = last
		return out
	case []Block:
		if len(v) == 0 {
			return v
		}
		out := append([]Block(nil), v...)
		out[len(out)-1].CacheControl = ephemeral
		return out
	}
	return content
}

// Retries: network errors, 5xx responses, and empty responses use up to maxAttempts; 429 uses up to
// maxRateLimitedAttempts with Retry-After when present, otherwise increasing backoff.
const (
	maxAttempts            = 3
	maxRateLimitedAttempts = 8
	maxBackoff             = 60 * time.Second
)

// backoffBase is the first delay between attempts; tests reduce it.
var backoffBase = 500 * time.Millisecond

type outcome int

const (
	fatal outcome = iota
	retry
	rateLimited
	cacheRejected
)

func (c *HTTPClient) Message(ctx context.Context, r Request) (Response, error) {
	r.Model = c.C.Model
	for attempt := 1; ; attempt++ {
		out, kind, wait, err := c.attempt(ctx, r, c.C.PromptCache && !c.noCache.Load())
		if err == nil {
			return out, nil
		}
		if kind == cacheRejected {
			// The gateway does not support cache markers: retry immediately without them and disable caching thereafter.
			attempt--
			continue
		}
		limit := maxAttempts
		if kind == rateLimited {
			limit = maxRateLimitedAttempts
		}
		if kind == fatal || attempt >= limit {
			return Response{}, err
		}
		if wait <= 0 {
			wait = backoff(attempt)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Response{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// attempt makes one request; outcome indicates whether to retry, and wait is the server-provided delay.
func (c *HTTPClient) attempt(ctx context.Context, r Request, cache bool) (Response, outcome, time.Duration, error) {
	payload, err := body(r, cache)
	if err != nil {
		return Response{}, fatal, 0, err
	}
	qctx, cancel := context.WithTimeout(ctx, c.C.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(qctx, "POST", c.C.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fatal, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.C.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, fatal, 0, ctx.Err()
		}
		return Response{}, retry, 0, err
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if closeErr := res.Body.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("failed to close the LLM API response: %w", closeErr)
	}
	if err != nil {
		return Response{}, retry, 0, fmt.Errorf("failed to read the LLM API response: %w", err)
	}
	status := res.StatusCode
	switch {
	case status >= 200 && status < 300:
		var out Response
		if err := json.Unmarshal(raw, &out); err != nil {
			return Response{}, fatal, 0, fmt.Errorf("invalid API response: %w", err)
		}
		if len(out.Content) == 0 && out.StopReason == "" {
			// An empty 200, such as when no model is running behind the gateway, is a failure rather than
			// a response; otherwise the agent silently spends every step on empty turns.
			return Response{}, retry, 0, fmt.Errorf("LLM API returned an empty response; is the model running behind the gateway?")
		}
		return out, fatal, 0, nil
	case cache && status == 400 && strings.Contains(string(raw), "cache_control"):
		c.noCache.Store(true)
		return Response{}, cacheRejected, 0, fmt.Errorf("gateway rejected cache_control")
	}
	err = fmt.Errorf("LLM API returned HTTP %d: %s", status, trunc(string(raw), 400))
	switch {
	case status == 429:
		return Response{}, rateLimited, retryAfter(res.Header.Get("Retry-After")), err
	case status >= 500:
		return Response{}, retry, 0, err
	}
	return Response{}, fatal, 0, err
}

// backoff is an increasing delay with up to 25% jitter; otherwise parallel tasks hit the gateway
// limit together and retry together.
func backoff(attempt int) time.Duration {
	d := backoffBase << (attempt - 1)
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	return d + time.Duration(rand.Int64N(int64(d/4)+1))
}

// retryAfter parses Retry-After as seconds or an HTTP date and caps it at maxBackoff.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = time.Until(at)
	}
	return min(max(d, 0), maxBackoff)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return strings.TrimSpace(s[:n]) + "…"
	}
	return strings.TrimSpace(s)
}
