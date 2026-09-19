package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Token                                                                        string
	BaseURL                                                                      string
	Model                                                                        string
	MaxSteps, MaxInputChars, MaxFileChars, MaxTotalContextChars, MaxOutputTokens int
	RequestTimeout, CommandTimeout                                               time.Duration
}

func Load() Config {
	return Config{
		Token: os.Getenv("MCP_LLM_TOKEN"), BaseURL: strings.TrimRight(os.Getenv("MCP_LLM_BASE_URL"), "/"), Model: os.Getenv("MCP_LLM_MODEL"),
		MaxSteps: envInt("MCP_MAX_AGENT_STEPS", 24, 1, 50), MaxInputChars: envInt("MCP_MAX_INPUT_CHARS", 12000, 256, 100000),
		MaxFileChars: envInt("MCP_MAX_FILE_CHARS", 24000, 1024, 200000), MaxTotalContextChars: envInt("MCP_MAX_TOTAL_CONTEXT_CHARS", 100000, 4096, 1000000),
		MaxOutputTokens: envInt("MCP_MAX_OUTPUT_TOKENS", 1600, 128, 16000), RequestTimeout: time.Duration(envInt("MCP_REQUEST_TIMEOUT_MS", 60000, 1000, 600000)) * time.Millisecond, CommandTimeout: time.Duration(envInt("MCP_COMMAND_TIMEOUT_MS", 60000, 1000, 600000)) * time.Millisecond,
	}
}
func (c Config) ValidateLLM() error {
	if c.Token == "" || c.BaseURL == "" || c.Model == "" {
		return fmt.Errorf("необходимо задать MCP_LLM_TOKEN, MCP_LLM_BASE_URL и MCP_LLM_MODEL")
	}
	return nil
}
func envInt(k string, d, min, max int) int {
	n, e := strconv.Atoi(os.Getenv(k))
	if e != nil || n < min || n > max {
		return d
	}
	return n
}
