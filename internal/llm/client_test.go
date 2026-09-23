package llm

import (
	"context"
	"github.com/wb/mcp-coder/internal/config"
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
		_, _ = w.Write([]byte(`{"content":[]}`))
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
