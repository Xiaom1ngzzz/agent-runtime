// Package context / layered.go —— 六层输入的确定性只读投影。见 ch04 §4.4。
//
// LayeredContextEngine 实现 ContextEngine 接口,把 SessionView 投影成 Context。
// **必须是确定性的只读投影**:不写状态、不读时钟、不发起 LLM/Memory 请求。
// 摘要/检索由 Compressor(ch04 §4.5)在上游产出 Event;Assemble 只读 State,
// 并可从 EventStore 展开 WorkingSet 指向的消息原文。
package context

import (
	stdctx "context"
	"fmt"

	"agent-runtime-go/domain"
	"agent-runtime-go/state"
)

// LayeredContextEngine 是 ch04 §4.4 的落地实现。
//
// 依赖:
//   - State:提供 SessionView(WorkingSet / Summaries / MemoryRefs / Progresses)。
//   - Store(可选):当 WorkingSet 里未被 Superseded 的 Turn 需要原文时,从 EventStore 拉。
//   - Instructions:Session 级 system prompt(六层第 1 层)。
//   - Tools:允许的工具集(六层第 6 层)。
//
// Assemble 不调 LLM、不写外部状态;唯一 IO 是确定性地只读 State/EventStore。
type LayeredContextEngine struct {
	State        state.State
	Store        state.EventStore // 用于取 WorkingSet 中未 Superseded 段的原文
	Instructions string           // 相对不变的 system prompt
	Tools        []domain.Tool    // 可用工具集
}

// Assemble 见 ch04 §4.4.1。
//
// 返回的 Messages 顺序:
//  1. system(Instructions)
//  2. system(TaskFrame:Goal + Budget + Constraints)
//  3. system(Progress:当前 Task 的进度快照,包 <task_progress> 标签)
//  4. system(Compressed History:摘要,包 <prior_summary> 标签)
//  5. user/assistant/tool 序列(Working Set 中未 Superseded 的 Turn 原文)
//  6. system(Memory Refs:参考资料,包 <memory_ref> 标签;靠近消息尾部)
func (e *LayeredContextEngine) Assemble(_ stdctx.Context, sessionID, taskID string) (domain.Context, error) {
	view, err := e.State.View(sessionID)
	if err != nil {
		return domain.Context{}, fmt.Errorf("state.View: %w", err)
	}

	msgs := make([]domain.Message, 0, 8)

	// 1. Instructions
	if e.Instructions != "" {
		msgs = append(msgs, domain.Message{Role: "system", Content: e.Instructions})
	}

	// 2. Task Frame
	if task, ok := view.Tasks[taskID]; ok {
		msgs = append(msgs, domain.Message{
			Role:    "system",
			Content: renderTaskFrame(task),
		})
	}

	// 3. Progress
	if progress, ok := view.Progresses[taskID]; ok {
		msgs = append(msgs, domain.Message{
			Role:    "system",
			Content: renderProgress(progress),
		})
	}

	// 4. Compressed History
	//    只放"覆盖 seq 落在 WorkingSet 之外的" Summary,避免与原文重复。
	minSeq := workingSetMinSeq(view.WorkingSet)
	for fromSeq, sum := range view.Summaries {
		if sum.TaskID != "" && taskID != "" && sum.TaskID != taskID {
			continue
		}
		if minSeq > 0 && sum.ToSeq >= minSeq {
			// 该 Summary 覆盖的段还在 WorkingSet 里(且未 Superseded 的部分会作为原文出现)
			// 跳过以避免重复;交给 Working Set 走原文
			_ = fromSeq
			continue
		}
		msgs = append(msgs, domain.Message{
			Role:    "system",
			Content: renderSummary(sum),
		})
	}

	// 5. Working Set:未 Superseded 的 Turn,展开原文。
	//    Superseded 的 Turn 已经在第 3 层作为 Summary 出现。
	if e.Store != nil {
		events, err := e.Store.Load(sessionID)
		if err != nil {
			return domain.Context{}, fmt.Errorf("store.Load: %w", err)
		}
		activeTurns := activeTurnSet(view.WorkingSet, taskID)
		for _, ev := range events {
			if _, ok := activeTurns[ev.TurnID]; !ok {
				continue
			}
			appendTurnMessage(&msgs, ev)
		}
	}

	// 6. Memory Refs:放在 Working Set 之后,使检索证据靠近消息尾部。
	for _, ref := range view.MemoryRefs {
		msgs = append(msgs, domain.Message{
			Role:    "system",
			Content: renderMemoryRef(ref),
		})
	}

	return domain.Context{
		SessionID: sessionID,
		TaskID:    taskID,
		Messages:  msgs,
		Tools:     e.Tools,
	}, nil
}

// ---------- helpers(全是纯函数) ----------

func renderTaskFrame(task domain.Task) string {
	return fmt.Sprintf("<task_frame>\ngoal: %s\nbudget_tokens: %d\n</task_frame>",
		task.Goal, task.Budget.MaxTokens)
}

func renderProgress(p domain.Progress) string {
	out := fmt.Sprintf("<task_progress version=%d updated_at=%q>\ngoal: %s\n",
		p.Version, p.UpdatedAt, p.Goal)
	for _, step := range p.Done {
		out += fmt.Sprintf("done: [%s] %s | %s | %s\n",
			step.Kind, step.Intent, step.Action, step.Observation)
	}
	for _, step := range p.Next {
		out += fmt.Sprintf("next: [%s] %s | %s\n", step.Kind, step.Intent, step.Action)
	}
	for _, loop := range p.Open {
		out += fmt.Sprintf("open: %s (raised_at=%s)\n", loop.Question, loop.RaisedAt)
	}
	out += "</task_progress>"
	return out
}

// renderSummary 把结构化 Summary 序列化成 LLM 可读的文本,包在 <prior_summary> 标签里。
// **保留 at_seq 让 LLM 能引用**,与 §4.6.2 的设计一致。
func renderSummary(s domain.Summary) string {
	out := "<prior_summary>\n"
	if len(s.UserIntents) > 0 {
		out += fmt.Sprintf("user_intents: %v\n", s.UserIntents)
	}
	if len(s.ToolResults) > 0 {
		out += fmt.Sprintf("tool_results: %v\n", s.ToolResults)
	}
	for _, d := range s.DecisionsMade {
		out += fmt.Sprintf("decision(@seq=%d): %s — %s\n", d.AtSeq, d.What, d.Why)
	}
	if len(s.OpenQuestions) > 0 {
		out += fmt.Sprintf("open_questions: %v\n", s.OpenQuestions)
	}
	if len(s.NextActions) > 0 {
		out += fmt.Sprintf("next_actions: %v\n", s.NextActions)
	}
	out += "</prior_summary>"
	return out
}

func renderMemoryRef(r domain.MemoryRef) string {
	return fmt.Sprintf("<memory_ref source=%q score=%.2f>\n%s\n</memory_ref>",
		r.Source, r.Score, r.Content)
}

func workingSetMinSeq(ws []domain.TurnDigest) int64 {
	var min int64
	for _, d := range ws {
		if d.Superseded {
			continue
		}
		if min == 0 || d.FromSeq < min {
			min = d.FromSeq
		}
	}
	return min
}

func activeTurnSet(ws []domain.TurnDigest, taskID string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, d := range ws {
		if d.Superseded {
			continue
		}
		if d.TaskID != "" && taskID != "" && d.TaskID != taskID {
			continue
		}
		out[d.TurnID] = struct{}{}
	}
	return out
}

// appendTurnMessage 只处理"直接对应 role"的 payload,其他事件在 Assemble 层不出现。
func appendTurnMessage(msgs *[]domain.Message, e domain.Event) {
	switch p := e.Payload.(type) {
	case domain.PayloadUserSpoke:
		*msgs = append(*msgs, domain.Message{Role: "user", Content: p.Text})
	case domain.PayloadLLMReplied:
		m := p.Assistant
		if m.Role == "" {
			m.Role = "assistant"
		}
		m.ToolCalls = p.ToolCalls
		*msgs = append(*msgs, m)
	case domain.PayloadToolReturned:
		*msgs = append(*msgs, domain.Message{
			Role: "tool", ToolCallID: p.CallID, Content: p.Content,
		})
	}
}
