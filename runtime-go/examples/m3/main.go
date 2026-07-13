// M3 最小 Agent:Planner 拆子 Task → 子 Task 各跑一轮工具 → Saga 关父 → Checkpoint 恢复 → Eval。
package main

import (
	stdctx "context"
	"fmt"
	"os"

	"agent-runtime-go/domain"
	"agent-runtime-go/eval"
	"agent-runtime-go/executor"
	"agent-runtime-go/planner"
	"agent-runtime-go/runtime"
	"agent-runtime-go/runtime/memfakes"
	"agent-runtime-go/state"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := stdctx.Background()
	store := memfakes.NewEventStore()
	st := memfakes.NewState()
	cps := state.NewMemCheckpointStore()

	reg := executor.NewRegistry()
	reg.Register(domain.Tool{Name: "weather"}, func(_ stdctx.Context, _ string) (string, error) {
		return `{"temp":26,"sky":"多云"}`, nil
	})
	reg.Register(domain.Tool{Name: "send_email"}, func(_ stdctx.Context, _ string) (string, error) {
		return `{"ok":true}`, nil
	})

	script := []domain.LLMResponse{
		{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{{ID: "c1", Name: "weather", Arguments: `{"city":"北京"}`}},
			TokensIn:  100, TokensOut: 20,
		},
		{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{{ID: "c2", Name: "send_email", Arguments: `{"to":"a@b.com"}`}},
			TokensIn:  120, TokensOut: 24,
		},
	}

	ex := executor.NewToolExecutor(store, reg)
	rt := &runtime.Runtime{
		EventStore: store,
		State:      st,
		Context:    memfakes.NewContextEngine(st, store, reg.Descriptions()),
		Prompt:     memfakes.PromptCompiler{},
		LLM:        memfakes.NewLLMProvider(script),
		Executor:   ex,
	}

	const sid, parent = "s1", "t1"
	appendEv := func(e domain.Event) error {
		e.SessionID = sid
		buf := []domain.Event{e}
		if err := store.Append(buf); err != nil {
			return err
		}
		return st.Apply(buf)
	}

	if err := appendEv(domain.Event{Type: domain.EvtSessionOpened, Payload: domain.PayloadSessionOpened{Principal: "user"}}); err != nil {
		return err
	}
	if err := appendEv(domain.Event{
		Type: domain.EvtTaskCreated, TaskID: parent,
		Payload: domain.PayloadTaskCreated{Goal: "查天气 + 发邮件", Budget: domain.Budget{MaxTokens: 8000}},
	}); err != nil {
		return err
	}

	pl := planner.NewGraphPlanner()
	view, err := st.View(sid)
	if err != nil {
		return err
	}
	planned, err := pl.Plan(ctx, view, parent)
	if err != nil {
		return err
	}
	for _, e := range planned {
		e.SessionID = sid
		if err := appendEv(e); err != nil {
			return err
		}
	}

	view, _ = st.View(sid)
	children := domain.BuildTaskGraph(view.Tasks).ChildrenOf(parent)
	fmt.Printf("spawned %d children: %v\n", len(children), children)

	for i, cid := range children {
		turn := fmt.Sprintf("r%d", i+1)
		if err := appendEv(domain.Event{
			Type: domain.EvtTurnStarted, TaskID: cid, TurnID: turn,
			Payload: domain.PayloadTurnStarted{Index: i},
		}); err != nil {
			return err
		}
		if _, err := rt.Step(ctx, sid, cid, turn); err != nil {
			return fmt.Errorf("step %s: %w", cid, err)
		}
		if err := appendEv(domain.Event{
			Type: domain.EvtTaskEnded, TaskID: cid,
			Payload: domain.PayloadTaskEnded{Status: domain.TaskSucceeded},
		}); err != nil {
			return err
		}
		if err := state.TakeCheckpoint(sid, st, cps); err != nil {
			return err
		}
	}

	view, _ = st.View(sid)
	ended, err := planner.SagaCoordinator{}.OnChildEnded(view, parent)
	if err != nil {
		return err
	}
	for _, e := range ended {
		if err := appendEv(e); err != nil {
			return err
		}
	}

	// 恢复验证
	fresh := memfakes.NewState()
	n, err := state.Recover(sid, cps, store, fresh)
	if err != nil {
		return err
	}
	got, _ := fresh.View(sid)
	fmt.Printf("recovered replayed=%d parent_status=%s\n", n, got.Tasks[parent].Status)

	// 教学 smoke:用独立切片模拟已审核 baseline 与本次 candidate。
	// 生产评测应从版本化 fixture 加载 baseline,不能由本次运行自动生成。
	baseline := store.Snapshot()
	candidate := append([]domain.Event(nil), baseline...)
	score := eval.CompareStreams(baseline, candidate, "")
	fmt.Printf("eval passed=%v tool_calls=%d tool_errors=%d\n", score.Passed, score.ToolCalls, score.ToolErrors)
	if got.Tasks[parent].Status != domain.TaskSucceeded {
		return fmt.Errorf("parent not succeeded")
	}
	return nil
}
