package mcpserver

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wb/mcp-coder/internal/agent"
	"github.com/wb/mcp-coder/internal/config"
	"github.com/wb/mcp-coder/internal/core"
	"github.com/wb/mcp-coder/internal/llm"
	"github.com/wb/mcp-coder/internal/runtime"
	"github.com/wb/mcp-coder/internal/tools"
	"github.com/wb/mcp-coder/internal/workspace"
	"net/http"
	"strings"
)

const instructions = "mcp-coder — исполнитель для экономии токенов основной модели: пишет код, документацию и тесты по подготовленному заданию. Передавайте в execute_coding_task конкретный результат, suggestedFiles, allowedChangePaths, критерии приёмки и краткий projectContext с уже найденными фактами. Объединяйте связанные правки в одну задачу. Не поручайте повторную разведку, если файлы и изменения уже известны. Для документации и точных механических замен без требуемой проверки явно задавайте requireVerification:false; для кода и тестов указывайте одну узкую релевантную verification. Архитектуру, неоднозначные решения и итоговый review оставляйте основной модели. В verification передавайте команду, объявленную самим репозиторием (скрипт package.json, цель Makefile/justfile/Taskfile или штатную команду тулчейна); аргументы — только пути внутри workspace. Успешный результат возвращает diff изменённых файлов. Результат компактный; подробные чтения и правки исполнитель делает сам. ask_coder — короткий read-only вопрос без изменений: пути в files читаются из workspace и прикладываются к вопросу, большой файл приходит усечённым окном."

type Ask struct {
	Question      string   `json:"question" jsonschema:"read-only question"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	Files         []string `json:"files,omitempty"`
}

const askFileLimit = 10

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
	mcp.AddTool(s, &mcp.Tool{Name: "ask_coder", Description: "Задать модели короткий вопрос без изменений workspace; файлы из files читаются и прикладываются к вопросу."}, func(ctx context.Context, _ *mcp.CallToolRequest, in Ask) (*mcp.CallToolResult, map[string]string, error) {
		current := manager.Snapshot()
		if e := current.ValidateLLM(); e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		if len(in.Question) == 0 || len(in.Question) > current.MaxInputChars {
			return nil, map[string]string{"status": "failed", "summary": "вопрос отсутствует или слишком велик"}, nil
		}
		root, e := workspace.Resolve(in.WorkspaceRoot)
		if e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		attached, warnings := attachFiles(current, root, in.Files)
		out, e := (&llm.HTTPClient{C: current, HTTP: &http.Client{Timeout: current.RequestTimeout}}).Message(ctx, llm.Request{System: "You are a concise read-only coding assistant. Answer from the attached file contents when they are present; a file may arrive as a truncated window. Do not claim to edit files or to read anything that was not attached.", MaxTokens: current.MaxOutputTokens, Messages: []llm.Message{{Role: "user", Content: attached + in.Question}}})
		if e != nil {
			return nil, map[string]string{"status": "failed", "summary": e.Error()}, nil
		}
		var text string
		for _, b := range out.Content {
			text += b.Text
		}
		answer := map[string]string{"status": "completed", "model": current.Model, "answer": text}
		if len(warnings) > 0 {
			answer["warnings"] = strings.Join(warnings, "; ")
		}
		return nil, answer, nil
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}

// Файлы вопроса читаются тем же безопасным путём, что и в задаче: ask_coder остаётся одним
// запросом к модели, поэтому содержимое прикладывается сразу, а не добывается инструментами.
func attachFiles(c config.Config, root string, files []string) (string, []string) {
	if len(files) == 0 {
		return "", nil
	}
	var warnings []string
	if len(files) > askFileLimit {
		warnings = append(warnings, fmt.Sprintf("приложены первые %d файлов из %d", askFileLimit, len(files)))
		files = files[:askFileLimit]
	}
	runner := tools.Runner{Root: root, MaxFile: c.MaxFileChars, MaxOutput: c.MaxFileChars}
	budget := c.MaxTotalContextChars / 2
	var b strings.Builder
	for _, path := range files {
		text, err := runner.ReadFile(path, 0, 0)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s", path, err))
			continue
		}
		if b.Len()+len(text) > budget {
			warnings = append(warnings, fmt.Sprintf("%s: не приложен, исчерпан бюджет контекста", path))
			continue
		}
		b.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", path, text))
	}
	if b.Len() == 0 {
		return "", warnings
	}
	return "Attached files:\n" + b.String() + "Question: ", warnings
}
func execute(ctx context.Context, c config.Config, in agent.Input) agent.Result {
	r, _ := core.CodingExecutor{Config: c}.ExecuteCodingTask(ctx, in)
	return r
}
