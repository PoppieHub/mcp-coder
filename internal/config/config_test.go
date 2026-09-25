package config

import (
	"testing"
	"time"
)

func TestLoadAndValidate(t *testing.T) {
	t.Setenv("MCP_LLM_TOKEN", "token")
	t.Setenv("MCP_LLM_BASE_URL", "https://example.test/")
	t.Setenv("MCP_LLM_MODEL", "model")
	t.Setenv("MCP_MAX_AGENT_STEPS", "7")
	c := Load()
	if c.Token != "token" || c.BaseURL != "https://example.test" || c.Model != "model" || c.MaxSteps != 7 {
		t.Fatalf("unexpected config: %+v", c)
	}
	if err := c.ValidateLLM(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"MCP_LLM_TOKEN", "MCP_LLM_BASE_URL", "MCP_LLM_MODEL"} {
		t.Setenv(key, "")
	}
	if err := Load().ValidateLLM(); err == nil {
		t.Fatal("missing configuration accepted")
	}
}

func TestInvalidLimitsUseDefaults(t *testing.T) {
	t.Setenv("MCP_MAX_AGENT_STEPS", "999")
	t.Setenv("MCP_MAX_TOTAL_CONTEXT_CHARS", "invalid")
	t.Setenv("MCP_MAX_OUTPUT_TOKENS", "0")
	c := Load()
	if c.MaxSteps != 24 || c.MaxTotalContextChars != 160000 || c.MaxOutputTokens != 3000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestTaskTimeoutConfig(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 10 * time.Minute}, {"invalid", 10 * time.Minute}, {"999", 10 * time.Minute}, {"3600001", 10 * time.Minute}, {"120000", 2 * time.Minute},
	} {
		t.Setenv("MCP_TASK_TIMEOUT_MS", tc.value)
		if got := Load().TaskTimeout; got != tc.want {
			t.Fatalf("%q: got %s want %s", tc.value, got, tc.want)
		}
	}
}
