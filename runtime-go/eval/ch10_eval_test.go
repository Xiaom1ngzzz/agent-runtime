package eval_test

import (
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/eval"
)

func TestCh10CompareStreams(t *testing.T) {
	golden := []domain.Event{
		{Type: domain.EvtTaskCreated, TaskID: "t1", Payload: domain.PayloadTaskCreated{Goal: "g"}},
		{Type: domain.EvtToolCalled, TaskID: "t1", Payload: domain.PayloadToolCalled{CallID: "c1", Name: "weather"}},
		{Type: domain.EvtToolReturned, TaskID: "t1", Payload: domain.PayloadToolReturned{CallID: "c1", Content: "ok"}},
		{Type: domain.EvtTaskEnded, TaskID: "t1", Payload: domain.PayloadTaskEnded{Status: domain.TaskSucceeded}},
	}
	actual := append([]domain.Event{}, golden...)
	s := eval.CompareStreams(golden, actual, "t1")
	if !s.Passed {
		t.Fatalf("expected pass, notes=%v", s.Notes)
	}
	if s.ToolCalls != 1 || s.ToolErrors != 0 {
		t.Fatalf("tools=%d errors=%d", s.ToolCalls, s.ToolErrors)
	}

	bad := append([]domain.Event{}, golden...)
	bad[3] = domain.Event{
		Type: domain.EvtTaskEnded, TaskID: "t1",
		Payload: domain.PayloadTaskEnded{Status: domain.TaskFailed},
	}
	s2 := eval.CompareStreams(golden, bad, "t1")
	if s2.Passed {
		t.Fatal("expected fail on status mismatch")
	}
}
