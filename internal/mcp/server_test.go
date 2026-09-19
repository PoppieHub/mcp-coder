package mcpserver

import (
	"context"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"testing"
)

func TestExecuteRejectsMissingConfiguration(t *testing.T) {
	r := execute(context.Background(), config.Config{}, agent.Input{Task: "x"})
	if r.Status != "failed" || r.Summary == "" {
		t.Fatalf("%+v", r)
	}
}
func TestExecuteRejectsInvalidWorkspace(t *testing.T) {
	c := config.Config{Token: "x", BaseURL: "https://example.test", Model: "model"}
	r := execute(context.Background(), c, agent.Input{Task: "x", WorkspaceRoot: "/definitely/not/a/workspace"})
	if r.Status != "failed" || r.Summary == "" {
		t.Fatalf("%+v", r)
	}
}
func TestMin(t *testing.T) {
	if min(1, 2) != 1 || min(3, 2) != 2 {
		t.Fatal("min")
	}
}
