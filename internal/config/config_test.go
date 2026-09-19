package config

import "testing"

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
	if got := Load().MaxSteps; got != 16 {
		t.Fatalf("got %d", got)
	}
}
