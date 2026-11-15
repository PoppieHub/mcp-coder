package llm

import (
	"context"
	"github.com/wb/mcp-coder/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(s *httptest.Server) *HTTPClient {
	return &HTTPClient{C: config.Config{Token: "secret-token", BaseURL: s.URL, Model: "test-model", RequestTimeout: time.Second}, HTTP: s.Client()}
}
func TestMessageSuccessAndHeaders(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("missing bearer token")
		}
		if r.URL.Path != "/v1/messages" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer s.Close()
	out, err := testClient(s).Message(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil || len(out.Content) != 1 || out.Content[0].Text != "ok" {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestMessageDoesNotRetryUnauthorizedOrLeakToken(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer s.Close()
	_, err := testClient(s).Message(context.Background(), Request{})
	if calls != 1 || err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestMessageRetriesTransientFailure(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer s.Close()
	if _, err := testClient(s).Message(context.Background(), Request{}); err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestMessageHonorsCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c := testClient(s)
	c.C.RequestTimeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Message(ctx, Request{})
	if err == nil {
		t.Fatal("cancelled request succeeded")
	}
}

func TestRetryBackoffHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	// Cancel during the 250ms retry delay, after the first response.
	defer func(old time.Duration) { backoffBase = old }(backoffBase)
	backoffBase = 250 * time.Millisecond
	c := testClient(s)
	c.HTTP.Transport = cancelAfterResponse{base: s.Client().Transport, cancel: cancel}
	start := time.Now()
	_, err := c.Message(ctx, Request{})
	if err != context.Canceled || calls != 1 || time.Since(start) >= 250*time.Millisecond {
		t.Fatalf("err=%v calls=%d elapsed=%s", err, calls, time.Since(start))
	}
}

type cancelAfterResponse struct {
	base   http.RoundTripper
	cancel context.CancelFunc
}

func (t cancelAfterResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(r)
	time.AfterFunc(20*time.Millisecond, t.cancel)
	return res, err
}

func TestCacheMarksDoNotLeakIntoHistory(t *testing.T) {
	results := []map[string]any{{"type": "tool_result", "tool_use_id": "1", "content": "x"}}
	r := Request{System: "sys", Tools: []Tool{{Name: "a"}, {Name: "b"}}, Messages: []Message{{Role: "user", Content: "task"}, {Role: "assistant", Content: []Block{{Type: "tool_use", ID: "1"}}}, {Role: "user", Content: results}}}
	raw, err := body(r, true)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Count(got, `"cache_control"`) != 3 || !strings.Contains(got, `"system":[{"cache_control"`) {
		t.Fatal(got)
	}
	if r.Tools[1].CacheControl != nil || results[0]["cache_control"] != nil {
		t.Fatal("cache mark written into the caller's request")
	}
	plain, _ := body(r, false)
	if strings.Contains(string(plain), "cache_control") || !strings.Contains(string(plain), `"system":"sys"`) {
		t.Fatal(string(plain))
	}
}

func TestGatewayRejectingCacheMarksFallsBackWithoutThem(t *testing.T) {
	var bodies []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(strings.Builder)
		_, _ = io.Copy(b, r.Body)
		bodies = append(bodies, b.String())
		if strings.Contains(b.String(), "cache_control") {
			http.Error(w, `{"error":"unknown field cache_control"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":5,"cache_read_input_tokens":7}}`))
	}))
	defer s.Close()
	c := testClient(s)
	c.C.PromptCache = true
	for i := 0; i < 2; i++ {
		out, err := c.Message(context.Background(), Request{System: "sys", Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil || out.Usage.CacheReadTokens != 7 {
			t.Fatalf("%+v %v", out, err)
		}
	}
	// Rejection costs one request; subsequent requests are sent without markers immediately.
	if len(bodies) != 3 || strings.Contains(bodies[2], "cache_control") {
		t.Fatalf("%d requests: %v", len(bodies), bodies)
	}
}

func TestPersistentFailureAndEmptyRepliesReturnErrors(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"HTTP 503":  func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusServiceUnavailable) },
		"empty 200": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"content":[]}`)) },
	} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; handler(w, r) }))
		_, err := testClient(s).Message(context.Background(), Request{})
		s.Close()
		if err == nil || calls != 3 {
			t.Fatalf("%s: calls=%d err=%v", name, calls, err)
		}
	}
}

func init() { backoffBase = time.Millisecond }

func TestRateLimitRetriesLongerAndHonorsRetryAfter(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 6 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer s.Close()
	if out, err := testClient(s).Message(context.Background(), Request{}); err != nil || calls != 6 || out.Content[0].Text != "ok" {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if got := retryAfter("7"); got != 7*time.Second {
		t.Fatal(got)
	}
	if got := retryAfter("3600"); got != maxBackoff {
		t.Fatal(got)
	}
	if got := retryAfter(time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)); got != 0 {
		t.Fatal(got)
	}
	if d := backoff(40); d < maxBackoff || d > maxBackoff+maxBackoff/4 {
		t.Fatal(d)
	}
}
