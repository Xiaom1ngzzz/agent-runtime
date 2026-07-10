// ch02 端到端集成测试:验证 Runtime.Step 产出与 ch01 手工样本一致的关键指标。
//
// 与 `runtime-rs/tests/ch02_end_to_end.rs` 逐字段对齐。
package main

import (
	stdctx "context"
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/runtime"
	"agent-runtime-go/runtime/memfakes"
)

func TestCh02EndToEndMatchesCh01Totals(t *testing.T) {
	ctx := stdctx.Background()
	store := memfakes.NewEventStore()

	tools := map[string]memfakes.ToolFunc{
		"weather":    func(_ string) (string, error) { return `{"temp":26,"sky":"多云"}`, nil },
		"send_email": func(_ string) (string, error) { return `{"ok":true}`, nil },
	}
	toolDescs := []domain.Tool{{Name: "weather"}, {Name: "send_email"}}

	script := []domain.LLMResponse{
		{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{{ID: "c1", Name: "weather",
				Arguments: `{"city":"北京","date":"2026-07-10"}`}},
			TokensIn: 520, TokensOut: 48,
		},
		{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{{ID: "c2", Name: "send_email",
				Arguments: `{"to":"alice@example.com","body":"..."}`}},
			TokensIn: 610, TokensOut: 72,
		},
		{
			Assistant: domain.Message{Role: "assistant", Content: "已经发送提醒邮件给 Alice。"},
			TokensIn:  700, TokensOut: 20,
		},
	}

	st := memfakes.NewState()
	rt := &runtime.Runtime{
		EventStore: store,
		State:      st,
		Context:    memfakes.NewContextEngine(st, store, toolDescs),
		Prompt:     memfakes.PromptCompiler{},
		LLM:        memfakes.NewLLMProvider(script),
		Executor:   memfakes.NewExecutor(store, tools),
	}

	const sid, tid = "s1", "t1"
	mustT := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	mustT(appendAll(rt, sid, "", "",
		event(domain.EvtSessionOpened, domain.PayloadSessionOpened{Principal: "user-42"}),
		event(domain.EvtUserSpoke, domain.PayloadUserSpoke{Text: "查天气 + 发邮件"}),
	))
	mustT(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskCreated, domain.PayloadTaskCreated{
			Goal: "查天气 + 发邮件", Budget: domain.Budget{MaxTokens: 8000},
		}),
	))

	for i, turnID := range []string{"r1", "r2", "r3"} {
		mustT(appendAll(rt, sid, tid, turnID,
			event(domain.EvtTurnStarted, domain.PayloadTurnStarted{Index: i}),
		))
		if _, err := rt.Step(ctx, sid, tid, turnID); err != nil {
			t.Fatalf("step %s: %v", turnID, err)
		}
	}
	mustT(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskEnded, domain.PayloadTaskEnded{Status: domain.TaskSucceeded}),
	))

	events := store.Snapshot()
	if got, want := len(events), 20; got != want {
		t.Fatalf("event count: got %d, want %d", got, want)
	}

	view, err := rt.State.View(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[tid].Status; got != domain.TaskSucceeded {
		t.Fatalf("task status: got %s, want succeeded", got)
	}
	last := view.LastTurn[tid]
	if last.ID != "r3" || last.Index != 2 || last.Status != domain.TurnDone {
		t.Fatalf("last turn: %+v", last)
	}

	var totalIn int
	for _, e := range events {
		if p, ok := e.Payload.(domain.PayloadTurnEnded); ok {
			totalIn += p.TokensIn
		}
	}
	if totalIn != 1830 {
		t.Fatalf("tokens_in sum: got %d, want 1830", totalIn)
	}
}
