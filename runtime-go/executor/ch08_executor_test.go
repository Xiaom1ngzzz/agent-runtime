package executor_test

// TestCh08ToolExecutor 是 ch08 承诺的端到端证据:
//
//  1. 已知工具 → ToolCalled + ToolReturned;
//  2. 未知工具 → ToolBindFailed + ToolReturned{IsError};
//  3. 超时 → ToolReturned{IsError, timeout}。

import (
	stdctx "context"
	"testing"
	"time"

	"agent-runtime-go/domain"
	"agent-runtime-go/executor"
	"agent-runtime-go/runtime/memfakes"
)

func TestCh08ToolExecutor(t *testing.T) {
	store := memfakes.NewEventStore()
	reg := executor.NewRegistry()
	reg.Register(domain.Tool{Name: "weather"}, func(_ stdctx.Context, _ string) (string, error) {
		return `{"temp":26}`, nil
	})
	reg.Register(domain.Tool{Name: "slow"}, func(ctx stdctx.Context, _ string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return "ok", nil
		}
	})

	ex := executor.NewToolExecutor(store, reg)
	ex.Timeout = 50 * time.Millisecond

	const sid, tid, turn = "s1", "t1", "r1"
	mustAppend := func(e domain.Event) {
		t.Helper()
		e.SessionID, e.TaskID, e.TurnID = sid, tid, turn
		buf := []domain.Event{e}
		if err := store.Append(buf); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(domain.Event{
		Type: domain.EvtLLMReplied,
		Payload: domain.PayloadLLMReplied{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{
				{ID: "c1", Name: "weather", Arguments: `{"city":"BJ"}`},
				{ID: "c2", Name: "nope", Arguments: `{}`},
				{ID: "c3", Name: "slow", Arguments: `{}`},
			},
		},
	})

	evs, err := ex.Run(stdctx.Background(), domain.Turn{ID: turn, TaskID: tid})
	if err != nil {
		t.Fatal(err)
	}

	var gotBindFail, gotWeatherOK, gotTimeout bool
	for _, e := range evs {
		switch p := e.Payload.(type) {
		case domain.PayloadToolBindFailed:
			if p.Name == "nope" && p.Reason == "unknown_tool" {
				gotBindFail = true
			}
		case domain.PayloadToolReturned:
			if p.CallID == "c1" && !p.IsError && p.Content == `{"temp":26}` {
				gotWeatherOK = true
			}
			if p.CallID == "c3" && p.IsError {
				gotTimeout = true
			}
		}
	}
	if !gotWeatherOK {
		t.Fatal("expected weather success")
	}
	if !gotBindFail {
		t.Fatal("expected ToolBindFailed for unknown tool")
	}
	if !gotTimeout {
		t.Fatal("expected timeout error for slow tool")
	}
}
