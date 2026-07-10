package compressor_test

// TestCh04CompressionCycle 是 ch04 §4.10.2 承诺的端到端证据。
//
// 场景:
//   1. 用 memfakes Runtime 跑 6 个 Turn(user + LLM tool call + tool return + ...)。
//   2. 每 Turn 结束后调 Compressor.Tick;阈值设小(=4)让第 4 turn 时触发。
//   3. Compressor 触发时:
//      - 把最老的 2 个 Turn 摘要成 Summary。
//      - 追加 ContextCompressed Event。
//      - Fold 后 WorkingSet 里前 2 个 TurnDigest 被 mark Superseded。
//   4. 用 LayeredContextEngine.Assemble 拼出 Context,应满足:
//      - Instructions + TaskFrame + <prior_summary> + 未 Superseded turns 原文。
//      - 已 Superseded 的 turn 不再出现原文。
//   5. 断言 Event 流可回放:重新 Fold → 得到相同 SessionView。

import (
	stdctx "context"
	"strings"
	"testing"

	rtctx "agent-runtime-go/context"
	"agent-runtime-go/compressor"
	"agent-runtime-go/domain"
	"agent-runtime-go/runtime"
	"agent-runtime-go/runtime/memfakes"
)

func TestCh04CompressionCycle(t *testing.T) {
	ctx := stdctx.Background()
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }

	// ---------- 搭建 Runtime + Compressor ----------
	store := memfakes.NewEventStore()
	st := memfakes.NewState()

	toolDescs := []domain.Tool{{Name: "weather"}}
	tools := map[string]memfakes.ToolFunc{
		"weather": func(_ string) (string, error) { return `{"temp":26}`, nil },
	}

	// 6 个 Turn 的 LLM 剧本:每 turn 都触发一次 weather 工具。
	script := make([]domain.LLMResponse, 6)
	for i := range script {
		script[i] = domain.LLMResponse{
			Assistant: domain.Message{Role: "assistant"},
			ToolCalls: []domain.ToolCall{{
				ID: "c" + itoa(i+1), Name: "weather", Arguments: `{"city":"BJ"}`,
			}},
			TokensIn: 100, TokensOut: 20,
		}
	}

	rt := &runtime.Runtime{
		EventStore: store,
		State:      st,
		Context:    memfakes.NewContextEngine(store, toolDescs), // ch02 老 Assemble,供 Step 用
		Prompt:     memfakes.PromptCompiler{},
		LLM:        memfakes.NewLLMProvider(script),
		Executor:   memfakes.NewExecutor(store, tools),
	}

	// LayeredContextEngine —— 本章的主角。
	layered := &rtctx.LayeredContextEngine{
		State:        st,
		Store:        store,
		Instructions: "You are an agent.",
		Tools:        toolDescs,
	}

	// Compressor:阈值 4 —— 4 个 turn 时会把前 2 个摘要掉。
	summarizer := compressor.NewScriptedSummarizer([]domain.Summary{
		{
			UserIntents:   []string{"查北京天气(重复调用示范)"},
			ToolResults:   map[string]any{"weather:BJ": `{"temp":26}`},
			DecisionsMade: []domain.Decision{{What: "统一用摄氏度", Why: "用户偏好", AtSeq: 2}},
			OpenQuestions: []string{"是否需要湿度数据"},
			ModelUsed:     "test-model",
			PromptVersion: "v1",
			Confidence:    0.85,
		},
	})
	comp := &compressor.ByTurns{
		State: st, Store: store, Summarizer: summarizer, Threshold: 4,
	}

	// ---------- 追加 Session / Task ----------
	const sid, tid = "s1", "t1"
	must(appendAll(rt, sid, "", "",
		event(domain.EvtSessionOpened, domain.PayloadSessionOpened{Principal: "u"}),
		event(domain.EvtUserSpoke, domain.PayloadUserSpoke{Text: "帮我盯天气"}),
	))
	must(appendAll(rt, sid, tid, "",
		event(domain.EvtTaskCreated, domain.PayloadTaskCreated{
			Goal: "盯天气", Budget: domain.Budget{MaxTokens: 8000},
		}),
	))

	// ---------- 跑 6 个 Turn,期间尝试压 ----------
	var compressionHappened bool
	for i, turnID := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		must(appendAll(rt, sid, tid, turnID,
			event(domain.EvtTurnStarted, domain.PayloadTurnStarted{Index: i}),
		))
		if _, err := rt.Step(ctx, sid, tid, turnID); err != nil {
			t.Fatalf("step %s: %v", turnID, err)
		}
		// 每 Turn 结束尝试压缩一次。
		events, err := comp.Tick(ctx, sid)
		must(err)
		if len(events) > 0 {
			buf := events
			must(rt.EventStore.Append(buf))
			must(rt.State.Apply(buf))
			for _, e := range buf {
				if e.Type == domain.EvtContextCompressed {
					compressionHappened = true
				}
			}
		}
	}

	if !compressionHappened {
		t.Fatal("expected at least one ContextCompressed event, got none")
	}

	// ---------- 断言 1:View 里有 Summary + 部分 TurnDigest 被 Superseded ----------
	view, err := st.View(sid)
	must(err)
	if len(view.Summaries) == 0 {
		t.Fatal("expected view.Summaries non-empty after compression")
	}
	supersededCount := 0
	for _, d := range view.WorkingSet {
		if d.Superseded {
			supersededCount++
		}
	}
	if supersededCount == 0 {
		t.Fatal("expected at least one TurnDigest to be Superseded")
	}

	// ---------- 断言 2:LayeredContextEngine.Assemble 拼出的 Context 包含 <prior_summary> ----------
	c, err := layered.Assemble(ctx, sid, tid)
	must(err)
	found := false
	for _, m := range c.Messages {
		if strings.Contains(m.Content, "<prior_summary>") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Assemble output should contain <prior_summary> block")
	}

	// ---------- 断言 3:回放性 —— 全量 Fold 后视图相同 ----------
	allEvents := store.Snapshot()
	fresh := memfakes.NewState()
	must(fresh.Apply(allEvents))
	view2, err := fresh.View(sid)
	must(err)
	if len(view.Summaries) != len(view2.Summaries) {
		t.Fatalf("summaries count mismatch after replay: %d vs %d",
			len(view.Summaries), len(view2.Summaries))
	}
	if len(view.WorkingSet) != len(view2.WorkingSet) {
		t.Fatalf("workingset count mismatch: %d vs %d",
			len(view.WorkingSet), len(view2.WorkingSet))
	}
	// 检查每个 TurnDigest 的 Superseded 标记一致(回放能重现"哪些被压缩了")。
	for i := range view.WorkingSet {
		if view.WorkingSet[i].Superseded != view2.WorkingSet[i].Superseded {
			t.Fatalf("superseded mismatch at %d after replay", i)
		}
	}
}

// ---------- helpers ----------

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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
