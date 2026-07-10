package domain

import "time"

// Ch01Sample 是第一章 §1.6 用的黄金 Event 流："查天气 + 发邮件"。
// 结构固定，用于教学与测试；不要在此新增 Event。
func Ch01Sample() []Event {
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	at := func(offsetSec int) time.Time { return t0.Add(time.Duration(offsetSec) * time.Second) }

	const (
		sid = "s1"
		tid = "t1"
		r1  = "r1"
		r2  = "r2"
		r3  = "r3"
	)

	return []Event{
		{ID: "e01", SessionID: sid, Type: EvtSessionOpened, TS: at(0),
			Payload: PayloadSessionOpened{Principal: "user-42"}},

		{ID: "e02", SessionID: sid, Type: EvtUserSpoke, TS: at(1), CausedBy: "e01",
			Payload: PayloadUserSpoke{Text: "帮我查一下明天北京的天气，然后写一封提醒邮件给 alice@example.com"}},

		{ID: "e03", SessionID: sid, TaskID: tid, Type: EvtTaskCreated, TS: at(1), CausedBy: "e02",
			Payload: PayloadTaskCreated{Goal: "查天气 + 发邮件", Budget: Budget{MaxTokens: 8000}}},

		// ---- Turn 1: 决定查天气 ----
		{ID: "e04", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtTurnStarted, TS: at(2),
			Payload: PayloadTurnStarted{Index: 0}},
		{ID: "e05", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtLLMRequested, TS: at(2),
			Payload: PayloadLLMRequested{Model: "claude-opus-4-7"}},
		{ID: "e06", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtLLMReplied, TS: at(3), CausedBy: "e05",
			Payload: PayloadLLMReplied{
				Assistant: Message{Role: "assistant"},
				ToolCalls: []ToolCall{{ID: "c1", Name: "weather", Arguments: `{"city":"北京","date":"2026-07-10"}`}},
				TokensIn:  520, TokensOut: 48,
			}},
		{ID: "e07", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtToolCalled, TS: at(3), CausedBy: "e06",
			Payload: PayloadToolCalled{CallID: "c1", Name: "weather", Arguments: `{"city":"北京","date":"2026-07-10"}`}},
		{ID: "e08", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtToolReturned, TS: at(4), CausedBy: "e07",
			Payload: PayloadToolReturned{CallID: "c1", Content: `{"temp":26,"sky":"多云"}`}},
		{ID: "e09", SessionID: sid, TaskID: tid, TurnID: r1, Type: EvtTurnEnded, TS: at(4),
			Payload: PayloadTurnEnded{Status: TurnDone, TokensIn: 520, TokensOut: 48, LatencyMS: 2100}},

		// ---- Turn 2: 决定发邮件 ----
		{ID: "e10", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtTurnStarted, TS: at(5),
			Payload: PayloadTurnStarted{Index: 1}},
		{ID: "e11", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtLLMRequested, TS: at(5),
			Payload: PayloadLLMRequested{Model: "claude-opus-4-7"}},
		{ID: "e12", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtLLMReplied, TS: at(6), CausedBy: "e11",
			Payload: PayloadLLMReplied{
				Assistant: Message{Role: "assistant"},
				ToolCalls: []ToolCall{{ID: "c2", Name: "send_email", Arguments: `{"to":"alice@example.com","body":"..."}`}},
				TokensIn:  610, TokensOut: 72,
			}},
		{ID: "e13", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtToolCalled, TS: at(6), CausedBy: "e12",
			Payload: PayloadToolCalled{CallID: "c2", Name: "send_email", Arguments: `{"to":"alice@example.com","body":"..."}`}},
		{ID: "e14", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtToolReturned, TS: at(7), CausedBy: "e13",
			Payload: PayloadToolReturned{CallID: "c2", Content: `{"ok":true}`}},
		{ID: "e15", SessionID: sid, TaskID: tid, TurnID: r2, Type: EvtTurnEnded, TS: at(7),
			Payload: PayloadTurnEnded{Status: TurnDone, TokensIn: 610, TokensOut: 72, LatencyMS: 1800}},

		// ---- Turn 3: 收尾 ----
		{ID: "e16", SessionID: sid, TaskID: tid, TurnID: r3, Type: EvtTurnStarted, TS: at(8),
			Payload: PayloadTurnStarted{Index: 2}},
		{ID: "e17", SessionID: sid, TaskID: tid, TurnID: r3, Type: EvtLLMReplied, TS: at(9),
			Payload: PayloadLLMReplied{
				Assistant: Message{Role: "assistant", Content: "已经发送提醒邮件给 Alice。"},
				TokensIn:  700, TokensOut: 20,
			}},
		{ID: "e18", SessionID: sid, TaskID: tid, TurnID: r3, Type: EvtTurnEnded, TS: at(9),
			Payload: PayloadTurnEnded{Status: TurnDone, TokensIn: 700, TokensOut: 20, LatencyMS: 900}},

		{ID: "e19", SessionID: sid, TaskID: tid, Type: EvtTaskEnded, TS: at(9), CausedBy: "e17",
			Payload: PayloadTaskEnded{Status: TaskSucceeded}},
	}
}

// FoldSample 从 Ch01Sample 折叠出一个 SessionView。
// 这是第 3 章 State.Apply 的最小实现雏形——这里只覆盖样本用到的 EventType，
// 完整的 fold 逻辑在 ch03 落地。
func FoldSample(events []Event) SessionView {
	view := SessionView{
		Tasks:    map[string]Task{},
		LastTurn: map[string]Turn{},
	}
	for _, e := range events {
		switch p := e.Payload.(type) {
		case PayloadSessionOpened:
			view.Session = Session{ID: e.SessionID, Principal: p.Principal, CreatedAt: e.TS, LastActiveAt: e.TS}
		case PayloadTaskCreated:
			view.Tasks[e.TaskID] = Task{ID: e.TaskID, SessionID: e.SessionID, Goal: p.Goal, Status: TaskRunning, Budget: p.Budget, StartedAt: e.TS}
		case PayloadTaskEnded:
			t := view.Tasks[e.TaskID]
			t.Status = p.Status
			t.EndedAt = e.TS
			view.Tasks[e.TaskID] = t
		case PayloadTurnStarted:
			view.LastTurn[e.TaskID] = Turn{ID: e.TurnID, TaskID: e.TaskID, Index: p.Index, Status: TurnRunning}
		case PayloadTurnEnded:
			turn := view.LastTurn[e.TaskID]
			turn.Status = p.Status
			turn.TokensIn = p.TokensIn
			turn.TokensOut = p.TokensOut
			turn.CostUS = p.CostUS
			turn.LatencyMS = p.LatencyMS
			view.LastTurn[e.TaskID] = turn
		}
		if !e.TS.IsZero() {
			view.Session.LastActiveAt = e.TS
		}
	}
	return view
}
