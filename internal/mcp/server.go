package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/runtime"
	"net/http"
)

const instructions = "mcp-coder — исполнитель для экономии токенов основной модели: пишет код, документацию и тесты по подготовленному заданию. Передавайте в execute_coding_task конкретный результат, suggestedFiles, allowedChangePaths, критерии приёмки и краткий projectContext с уже найденными фактами. Объединяйте связанные правки в одну задачу. Не поручайте повторную разведку, если файлы и изменения уже известны. Для документации и точных механических замен без требуемой проверки явно задавайте requireVerification:false; для кода и тестов указывайте одну узкую релевантную verification. Архитектуру, неоднозначные решения и итоговый review оставляйте основной модели. Результат компактный; подробные чтения и правки исполнитель делает сам. ask_coder — короткий read-only вопрос без изменений."

type Ask struct {
	Question      string   `json:"question" jsonschema:"read-only question"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	Files         []string `json:"files,omitempty"`
}

func Run(ctx context.Context, c config.Config) error {
	manager := runtime.New(c)
	s := mcp.NewServer(&mcp.Implementation{Name: "mcp-coder", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: instructions})
	mcp.AddTool(s, &mcp.Tool{Name: "execute_coding_task", Description: "Написать код, документацию или тесты по конкретному заданию и списку файлов; вернуть компактный результат."}, func(ctx context.Context, _ *mcp.CallToolRequest, in agent.Input) (*mcp.CallToolResult, agent.Result, error) {
		result, err := manager.Execute(ctx, in)
		return nil, result, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_settings", Description: "Показать текущие runtime-настройки без токена."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, runtime.SettingsView, error) {
		return nil, manager.Settings(), nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "update_settings", Description: "Изменить runtime-настройки для следующих задач; токен никогда не возвращается."}, func(_ context.Context, _ *mcp.CallToolRequest, in runtime.Settings) (*mcp.CallToolResult, runtime.SettingsView, error) {
		out, e := manager.Update(in)
		if e != nil {
			return nil, runtime.SettingsView{}, e
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_status", Description: "Показать текущий статус задачи и usage процесса."}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, runtime.Status, error) {
		return nil, manager.Status(), nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "cancel_task", Description: "Отменить текущую задачу по ID или без ID."}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		TaskID string `json:"taskId,omitempty"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		ok := manager.Cancel(in.TaskID)
		return nil, map[string]any{"cancelled": ok}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_diagnostics", Description: "Проверить runtime-конфигурацию; api=true выполняет безопасный API diagnostic."}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		API bool `json:"api,omitempty"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := manager.Diagnostics(context.Background(), in.API)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "ask_coder", Description: "Задать модели короткий вопрос без изменений workspace."}, func(ctx context.Context, _ *mcp.CallToolRequest, in Ask) (*mcp.CallToolResult, map[string]string, error) {
		current := manager.Snapshot()
		if e := current.ValidateLLM(); e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		if len(in.Question) == 0 || len(in.Question) > current.MaxInputChars {
			return nil, map[string]string{"status": "failed", "summary": "вопрос отсутствует или слишком велик"}, nil
		}
		out, e := (&llm.HTTPClient{C: current, HTTP: &http.Client{Timeout: current.RequestTimeout}}).Message(ctx, llm.Request{System: "You are a concise read-only coding assistant. Do not claim to edit files.", MaxTokens: current.MaxOutputTokens, Messages: []llm.Message{{Role: "user", Content: in.Question}}})
		if e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		var text string
		for _, b := range out.Content {
			text += b.Text
		}
		return nil, map[string]string{"status": "completed", "model": current.Model, "answer": text}, nil
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}
func execute(ctx context.Context, c config.Config, in agent.Input) agent.Result {
	r, _ := core.CodingExecutor{Config: c}.ExecuteCodingTask(ctx, in)
	return r
}
