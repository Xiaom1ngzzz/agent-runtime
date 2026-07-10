// ch02 · 端到端跑一次"查天气 + 发邮件":这次通过 Runtime.Step 生成事件,
// 而不是像 ch01 那样手写。产出对齐 ch01 样本:19 条 Event、3 个 Turn、tokens_in=1830。
//
// 运行:
//   cd runtime-go && go run ./examples/ch02
package main

import (
	stdctx "context"
	"encoding/json"
	"fmt"

	"agent-runtime-go/domain"
	"agent-runtime-go/runtime"
	"agent-runtime-go/runtime/memfakes"
)

func main() {
	ctx := stdctx.Background()
	store := memfakes.NewEventStore()

	// ---- 定义"工具"(其实是脚本化函数) ----
	tools := map[string]memfakes.ToolFunc{
		"weather": func(_ string) (string, error) {
			return `{"temp":26,"sky":"多云"}`, nil
		},
		"send_email": func(_ string) (string, error) {
			return `{"ok":true}`, nil
		},
	}
	toolDescs := []domain.Tool{
		{Name: "weather", Description: "查天气"},
		{Name: "send_email", Description: "发邮件"},
	}

	// ---- LLM 剧本:三个 Turn 的固定响应 ----
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

	rt := &runtime.Runtime{
		EventStore: store,
		State:      memfakes.NewState(),
		Context:    memfakes.NewContextEngine(store, toolDescs),
		Prompt:     memfakes.PromptCompiler{},
		LLM:        memfakes.NewLLMProvider(script),
		Executor:   memfakes.NewExecutor(store, tools),
	}

	// ---- 预置:Session/Task 生命周期由调用方追加(见 ch03 State 一章) ----
	const sid, tid = "s1", "t1"
	must(appendAll(rt, sid, "", "",
		event(domain.EvtSessionOpened, domain.PayloadSessionOpened{Principal: "user-42"}),
		event(domain.EvtUserSpoke, domain.PayloadUserSpoke{
			Text: "帮我查一下明天北京的天气,然后写一封提醒邮件给 alice@example.com",
		}),
	))
	must(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskCreated, domain.PayloadTaskCreated{
			Goal: "查天气 + 发邮件", Budget: domain.Budget{MaxTokens: 8000},
		}),
	))

	// ---- 跑 3 个 Turn ----
	for i, turnID := range []string{"r1", "r2", "r3"} {
		must(appendAll(rt, sid, tid, turnID,
			event(domain.EvtTurnStarted, domain.PayloadTurnStarted{Index: i}),
		))
		if _, err := rt.Step(ctx, sid, tid, turnID); err != nil {
			panic(err)
		}
	}
	must(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskEnded, domain.PayloadTaskEnded{Status: domain.TaskSucceeded}),
	))

	// ---- 汇总输出 ----
	events := store.Snapshot()
	fmt.Printf("== Event 流(%d条) ==\n", len(events))
	for _, e := range events {
		fmt.Printf("  %-3s %-20s session=%s task=%-3s turn=%-3s\n",
			e.ID, e.Type, e.SessionID, dashIfEmpty(e.TaskID), dashIfEmpty(e.TurnID))
	}
	fmt.Println()

	view, _ := rt.State.View(sid)
	fmt.Println("== 折叠后的 SessionView ==")
	fmt.Printf("  session:  id=%s principal=%s\n", view.Session.ID, view.Session.Principal)
	for _, task := range view.Tasks {
		fmt.Printf("  task:     id=%s goal=%q status=%s\n", task.ID, task.Goal, task.Status)
	}
	for _, turn := range view.LastTurn {
		fmt.Printf("  turn:     task=%s id=%s index=%d status=%s tokens_in=%d tokens_out=%d\n",
			turn.TaskID, turn.ID, turn.Index, turn.Status, turn.TokensIn, turn.TokensOut)
	}

	var totalIn int
	for _, e := range events {
		if p, ok := e.Payload.(domain.PayloadTurnEnded); ok {
			totalIn += p.TokensIn
		}
	}
	fmt.Printf("  total tokens_in: %d\n", totalIn)

	// 让 example 也顺便产出一份 JSON,便于跨语言比对(ch03 会用)
	_ = json.NewEncoder(devnull{}).Encode(events)
}

// ---- helpers ----

type devnull struct{}

func (devnull) Write(p []byte) (int, error) { return len(p), nil }

func event(t domain.EventType, p domain.EventPayload) domain.Event {
	return domain.Event{Type: t, Payload: p}
}

func appendAll(rt *runtime.Runtime, sid, tid, turnID string, evs ...domain.Event) error {
	for _, e := range evs {
		e.SessionID, e.TaskID, e.TurnID = sid, tid, turnID
		buf := []domain.Event{e}
		if err := rt.EventStore.Append(buf); err != nil {
			return err
		}
		if err := rt.State.Apply(buf); err != nil {
			return err
		}
	}
	return nil
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
