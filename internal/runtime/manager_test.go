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
