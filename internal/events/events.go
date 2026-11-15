package events

import "time"

type Type string

const (
	TaskStarted               Type = "task.started"
	ContextDiscoveryStarted   Type = "context.discovery.started"
	ContextDiscoveryCompleted Type = "context.discovery.completed"
	AgentStepStarted          Type = "agent.step.started"
	ToolStarted               Type = "tool.started"
	ToolCompleted             Type = "tool.completed"
	FileChanged               Type = "file.changed"
	VerificationStarted       Type = "verification.started"
	VerificationCompleted     Type = "verification.completed"
	TaskCompleted             Type = "task.completed"
	TaskFailed                Type = "task.failed"
)

// Event intentionally carries only bounded operational metadata, never prompts or tool output.
type Event struct {
	Type            Type
	Timestamp       time.Time
	TaskID, Message string
}
type Sink interface{ Emit(Event) }
type Noop struct{}

func (Noop) Emit(Event) {}
func Emit(s Sink, typ Type, taskID, message string) {
	if s != nil {
		safeEmit(s, Event{Type: typ, Timestamp: time.Now().UTC(), TaskID: taskID, Message: message})
	}
}
func safeEmit(s Sink, e Event) { defer func() { _ = recover() }(); s.Emit(e) }
