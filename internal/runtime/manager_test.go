package runtime

import (
	"github.com/wb/mcp-coder/internal/config"
	"sync"
	"testing"
	"time"
)

func TestUpdateIsConcurrentSafeAndRedactsToken(t *testing.T) {
	m := New(config.Config{Token: "secret", Model: "a", BaseURL: "https://x", MaxSteps: 2, RequestTimeout: time.Second, CommandTimeout: time.Second})
	n := 3
	if _, e := m.Update(Settings{Model: "b", Token: "new", MaxAgentSteps: &n}); e != nil {
		t.Fatal(e)
	}
	if v := m.Settings(); v.Model != "b" || !v.TokenConfigured {
		t.Fatalf("%+v", v)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.Settings(); _ = m.Status() }()
	}
	wg.Wait()
}
func TestCancelWithoutTask(t *testing.T) {
	if New(config.Config{}).Cancel("") {
		t.Fatal("cancelled absent task")
	}
}

func TestTaskTimeoutSettings(t *testing.T) {
	m := New(config.Load())
	valid := 120000
	v, err := m.Update(Settings{TaskTimeoutMs: &valid})
	if err != nil || v.TaskTimeoutMs != valid || m.Snapshot().TaskTimeout != 2*time.Minute {
		t.Fatalf("view=%+v err=%v", v, err)
	}
	for _, invalid := range []int{0, 999, 3600001} {
		if _, err := m.Update(Settings{Model: "should not apply", TaskTimeoutMs: &invalid}); err == nil {
			t.Fatal("accepted invalid timeout", invalid)
		}
		if m.Settings() != v {
			t.Fatal("invalid update changed settings")
		}
	}
}
