package events

import (
	"testing"
	"time"
)

type sink struct{ got []Event }

func (s *sink) Emit(e Event) { s.got = append(s.got, e) }

type panicSink struct{}

func (panicSink) Emit(Event) { panic("sink") }
func TestEmitIsBestEffort(t *testing.T) {
	s := &sink{}
	Emit(s, TaskStarted, "t", "start")
	if len(s.got) != 1 || s.got[0].Timestamp.After(time.Now().UTC()) {
		t.Fatal(s.got)
	}
	Emit(panicSink{}, TaskFailed, "t", "x")
}
