package state_test

// TestSnapshotReplay 是 ch03 §3.7.4 承诺的端到端证据:
//
//  1. 用 ch02 那份 20 条 Event 的场景喂给 Runtime;
//  2. 每追加一条 TurnEnded 就拍一个 Snapshot(§3.6.2 Turn 边界策略);
//  3. 丢弃"当前 State",从最新 Snapshot + LoadFrom 恢复;
//  4. 断言恢复出的 SessionView 与"从零 Fold 全部 20 条"相等;
//  5. 断言恢复只 replay 了 Turn 3 之后的少量事件。
//
// 另外两条断言:
//   - 未知 EventType 走 wire 层 unknown type 报错;
//   - Seq 逆序被 State.Apply 拒绝(§3.5.4)。

import (
	stdctx "context"
	"testing"

	"agent-runtime-go/domain"
	"agent-runtime-go/runtime"
	"agent-runtime-go/runtime/memfakes"
	"agent-runtime-go/state"
)

func TestSnapshotReplay(t *testing.T) {
	ctx := stdctx.Background()
	rt := newRuntimeCh02Scenario()

	const sid, tid = "s1", "t1"
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	// ---- 追加系统级 Event ----
	must(appendAll(rt, sid, "", "",
		event(domain.EvtSessionOpened, domain.PayloadSessionOpened{Principal: "user-42"}),
		event(domain.EvtUserSpoke, domain.PayloadUserSpoke{Text: "查天气 + 发邮件"}),
	))
	must(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskCreated, domain.PayloadTaskCreated{
			Goal: "查天气 + 发邮件", Budget: domain.Budget{MaxTokens: 8000},
		}),
	))

	// ---- 每个 Turn 结束时拍一个 Snapshot ----
	snapStore := state.NewMemSnapshotStore()
	for i, turnID := range []string{"r1", "r2", "r3"} {
		must(appendAll(rt, sid, tid, turnID,
			event(domain.EvtTurnStarted, domain.PayloadTurnStarted{Index: i}),
		))
		if _, err := rt.Step(ctx, sid, tid, turnID); err != nil {
			t.Fatalf("step %s: %v", turnID, err)
		}
		// TurnEnded 已由 Step 追加。此刻是"Turn 边界",拍快照。
		view, err := rt.State.View(sid)
		must(err)
		must(snapStore.Save(sid, state.Snapshot{Seq: view.MaxSeq, View: view}))
	}
	must(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskEnded, domain.PayloadTaskEnded{Status: domain.TaskSucceeded}),
	))

	// ---- 现在装作"重启":新起 State,从 Snapshot + LoadFrom 恢复 ----
	freshState := memfakes.NewState()
	snap, ok, err := snapStore.Latest(sid)
	must(err)
	if !ok {
		t.Fatal("expected latest snapshot")
	}

	// 把 snap.View 塞进 fresh state:模拟"从磁盘加载快照"。
	// 生产实现里 State 会有一个 LoadSnapshot 方法;memfakes 简单起见走 Apply-from-events 语义,
	// 快照直接作为初始 view 注入。
	freshState.LoadSnapshot(sid, snap.View)

	// LoadFrom 拿到快照之后的所有事件:e19 = TaskEnded(seq=20 之后追加的那一条)。
	store := rt.EventStore.(*memfakes.EventStore)
	remaining, err := store.LoadFrom(sid, snap.Seq)
	must(err)
	if len(remaining) < 1 {
		t.Fatalf("expected some events after snapshot; got 0 (snap.Seq=%d)", snap.Seq)
	}
	must(freshState.Apply(remaining))

	// ---- 断言 1:恢复出的 View 与"从零 Fold 全部 20 条"相等 ----
	recovered, err := freshState.View(sid)
	must(err)

	allEvents := store.Snapshot()
	fullState := memfakes.NewState()
	must(fullState.Apply(allEvents))
	full, err := fullState.View(sid)
	must(err)

	if !viewsEqual(recovered, full) {
		t.Fatalf("recovered view != full-fold view:\n  recovered=%+v\n  full=%+v", recovered, full)
	}

	// ---- 断言 2:恢复只 replay 了少量事件(理想情况 = TaskEnded 一条) ----
	if got := len(remaining); got > 3 {
		t.Fatalf("expected ≤3 events replayed after latest snapshot, got %d", got)
	}
}

// TestSnapshotReplay_WireUnknownType 见 wire_test.go 的 TestWireUnknownType;这里断言 State 层的行为。
// TestSnapshotReplay_SeqRegression 证明 §3.5.4 单调校验会拒绝逆序 seq。
func TestSnapshotReplay_SeqRegression(t *testing.T) {
	st := memfakes.NewState()
	const sid = "s1"
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	must(st.Apply([]domain.Event{
		{ID: "e01", SessionID: sid, Type: domain.EvtSessionOpened, Seq: 1,
			Payload: domain.PayloadSessionOpened{Principal: "u"}},
		{ID: "e02", SessionID: sid, Type: domain.EvtUserSpoke, Seq: 2,
			Payload: domain.PayloadUserSpoke{Text: "hi"}},
	}))
	err := st.Apply([]domain.Event{
		{ID: "e02b", SessionID: sid, Type: domain.EvtUserSpoke, Seq: 2, // 与已有 MaxSeq 相同,应拒绝
			Payload: domain.PayloadUserSpoke{Text: "regression"}},
	})
	if err == nil {
		t.Fatal("expected seq regression to be rejected")
	}
}

// ---- helpers ----

func newRuntimeCh02Scenario() *runtime.Runtime {
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

	return &runtime.Runtime{
		EventStore: store,
		State:      memfakes.NewState(),
		Context:    memfakes.NewContextEngine(store, toolDescs),
		Prompt:     memfakes.PromptCompiler{},
		LLM:        memfakes.NewLLMProvider(script),
		Executor:   memfakes.NewExecutor(store, tools),
	}
}

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

func viewsEqual(a, b domain.SessionView) bool {
	if a.Session.ID != b.Session.ID || a.Session.Principal != b.Session.Principal {
		return false
	}
	if len(a.Tasks) != len(b.Tasks) || len(a.LastTurn) != len(b.LastTurn) {
		return false
	}
	for k, va := range a.Tasks {
		vb, ok := b.Tasks[k]
		if !ok || va.Status != vb.Status || va.Goal != vb.Goal {
			return false
		}
	}
	for k, va := range a.LastTurn {
		vb, ok := b.LastTurn[k]
		if !ok || va.ID != vb.ID || va.Index != vb.Index || va.Status != vb.Status ||
			va.TokensIn != vb.TokensIn || va.TokensOut != vb.TokensOut {
			return false
		}
	}
	return true
}
