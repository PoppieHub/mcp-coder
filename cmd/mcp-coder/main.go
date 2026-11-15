package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/wb/mcp-coder/internal/bench"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/llm"
	mcpserver "github.com/wb/mcp-coder/internal/mcp"
	"github.com/wb/mcp-coder/internal/workspace"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

func main() {
	c := config.Load()
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		doctor(c)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "bench" {
		os.Exit(runBench(c, os.Args[2:]))
	}
	if e := mcpserver.Run(context.Background(), c); e != nil {
		fmt.Fprintln(os.Stderr, "mcp-coder:", e)
		os.Exit(1)
	}
}
func doctor(c config.Config) {
	bad := false
	for _, x := range []struct{ k, v string }{{"MCP_LLM_TOKEN", c.Token}, {"MCP_LLM_BASE_URL", c.BaseURL}, {"MCP_LLM_MODEL", c.Model}} {
		if x.v == "" {
			fmt.Println(x.k + ": not set")
			bad = true
		} else {
			fmt.Println(x.k + ": set")
		}
	}
	root, e := workspace.Resolve("")
	if e != nil {
		fmt.Println("workspace: unavailable")
		bad = true
	} else {
		fmt.Println("workspace:", root)
	}
	for _, p := range []string{"git", "rg"} {
		_, e := exec.LookPath(p)
		if e != nil {
			fmt.Println(p + ": unavailable")
		} else {
			fmt.Println(p + ": available")
		}
	}
	fmt.Println("API check: skipped (pass --api to check the Messages API and tool use)")
	if len(os.Args) > 2 && os.Args[2] == "--api" {
		if e := c.ValidateLLM(); e != nil {
			fmt.Println("api:", e)
			bad = true
		} else if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
			fmt.Println("API: invalid base URL")
			bad = true
		} else {
			if err := doctorAPI(c); err != nil {
				fmt.Println("API:", err)
				bad = true
			}
		}
	}
	if bad {
		os.Exit(1)
	}
}

func doctorAPI(c config.Config) error {
	client := &llm.HTTPClient{C: c, HTTP: nil}
	// HTTPClient uses the default transport only in this explicit diagnostic path.
	client.HTTP = &http.Client{Timeout: c.RequestTimeout}
	tool := llm.Tool{Name: "diagnostic_tool", Description: "Synthetic diagnostic tool. Return one call.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
	first, err := client.Message(context.Background(), llm.Request{System: "Call diagnostic_tool once, then wait for its result.", MaxTokens: 64, Messages: []llm.Message{{Role: "user", Content: "Run the diagnostic."}}, Tools: []llm.Tool{tool}})
	if err != nil {
		return err
	}
	var id string
	for _, b := range first.Content {
		if b.Type == "tool_use" && b.Name == "diagnostic_tool" {
			id = b.ID
			break
		}
	}
	if id == "" {
		return fmt.Errorf("gateway/model did not return tool_use")
	}
	second, err := client.Message(context.Background(), llm.Request{System: "Respond with a short final confirmation.", MaxTokens: 64, Messages: []llm.Message{{Role: "user", Content: []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}}})
	if err != nil {
		return err
	}
	if len(second.Content) == 0 {
		return fmt.Errorf("gateway/model did not return a final response after tool_result")
	}
	fmt.Println("API: OK")
	fmt.Println("model:", c.Model)
	fmt.Println("Messages API: OK")
	fmt.Println("tool use: OK")
	fmt.Println("tool result roundtrip: OK")
	return nil
}

// runBench runs the suite in benchmarks/README.md against a real model. Each task is a separate
// LLM API request using the token from the environment, so a run incurs provider usage.
func runBench(c config.Config, args []string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	repeat := fs.Int("repeat", 1, "number of times to run each task")
	only := fs.String("only", "", "comma-separated task IDs, for example B03,B11; empty means all")
	out := fs.String("out", "", "path for the JSON report")
	trace := fs.String("trace", "", "directory for the conversation trace of each run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := c.ValidateLLM(); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		return 2
	}
	var ids []string
	if *only != "" {
		ids = strings.Split(*only, ",")
	}
	cases, err := bench.Select(bench.Cases(), ids)
	if err != nil || *repeat < 1 {
		fmt.Fprintln(os.Stderr, "bench: invalid arguments:", err)
		return 2
	}
	outcomes := bench.Runner{Config: c, Log: os.Stdout, TraceDir: *trace}.Run(context.Background(), cases, *repeat)
	s := bench.Summarize(outcomes)
	fmt.Printf("\nmodel %s: runs %d, passed %d, false completed %d, median steps %d, median tokens %d\n", c.Model, s.Runs, s.Passed, s.FalseCompleted, s.MedianSteps, s.MedianTokens)
	for _, f := range s.GateFailures {
		fmt.Println("gate:", f)
	}
	if *out != "" {
		if err := bench.WriteReport(*out, bench.Report{Model: c.Model, Summary: s, Outcomes: outcomes}); err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			return 1
		}
	}
	if len(s.GateFailures) > 0 {
		return 1
	}
	return 0
}
