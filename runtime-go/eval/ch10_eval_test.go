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

	replaced := append([]domain.Event{}, golden...)
	replaced[1] = domain.Event{
		Type: domain.EvtProgressUpdated, TaskID: "t1",
		Payload: domain.PayloadProgressUpdated{TaskID: "t1"},
	}
	replaced[2] = domain.Event{
		Type: domain.EvtProgressUpdated, TaskID: "t1",
		Payload: domain.PayloadProgressUpdated{TaskID: "t1"},
	}
	s3 := eval.CompareStreams(golden, replaced, "t1")
	if s3.Passed || s3.EventSequenceMatch {
		t.Fatal("same-length but structurally different stream must fail")
	}

	unknown := []domain.Event{
		{Type: domain.EvtToolCalled, TaskID: "t1", Payload: domain.PayloadToolCalled{CallID: "c1", Name: "nope"}},
		{Type: domain.EvtToolBindFailed, TaskID: "t1", Payload: domain.PayloadToolBindFailed{CallID: "c1", Name: "nope"}},
		{Type: domain.EvtToolReturned, TaskID: "t1", Payload: domain.PayloadToolReturned{CallID: "c1", IsError: true}},
		{Type: domain.EvtTaskEnded, TaskID: "t1", Payload: domain.PayloadTaskEnded{Status: domain.TaskFailed}},
	}
	s4 := eval.CompareStreams(unknown, unknown, "t1")
	if !s4.Passed || s4.ToolErrors != 1 || s4.ToolErrorRate != 1 {
		t.Fatalf("one failed call should count once: %+v", s4)
	}

	if eval.CompareStreams(nil, nil, "t1").Passed {
		t.Fatal("missing terminal task must fail")
	}
}
