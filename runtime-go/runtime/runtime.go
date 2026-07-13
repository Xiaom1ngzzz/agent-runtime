// Package runtime 是把第 1 章 6 个接口串起来的协调器。
// 对应章节:ch02-runtime-dataflow.md §2.4。
package runtime

import (
	stdctx "context"
	"errors"
	"fmt"

	rtctx "agent-runtime-go/context"
	"agent-runtime-go/domain"
	"agent-runtime-go/executor"
	"agent-runtime-go/llm"
	"agent-runtime-go/prompt"
	"agent-runtime-go/state"
)

// Runtime 持有 6 个协作接口。
// 生命周期由调用方(上层 Loop)追加。协调器只负责"给定一个已就绪的 Turn,把它跑完"。
// Step 内无跨 Turn 可变状态;并发安全依赖底层 EventStore/State 实现(ch03 §3.4.2)。
type Runtime struct {
	EventStore state.EventStore
	State      state.State
	Context    rtctx.ContextEngine
	Prompt     prompt.PromptCompiler
	LLM        llm.LLMProvider
	Executor   executor.Executor
}

// Step 驱动一个 Turn 完成,产出这一 Turn 追加的 Event 数组。
//
// 数据流(见 §2.3):
//
//	Fold → Project → Compile → Chat → Emit
//
// 每一步失败都会 emit 一条终止 Event 并返回 error,不做隐式重试。
// 重试策略是调用方或上层 Loop 的事。
func (r *Runtime) Step(ctx stdctx.Context, sessionID, taskID, turnID string) ([]domain.Event, error) {
	var appended []domain.Event
	var lastAppendedID string
	if prior, err := r.EventStore.Load(sessionID); err == nil && len(prior) > 0 {
		lastAppendedID = prior[len(prior)-1].ID
	}
	append := func(e domain.Event) error {
		e.SessionID, e.TaskID, e.TurnID = sessionID, taskID, turnID
		if e.CausedBy == "" && lastAppendedID != "" {
			e.CausedBy = lastAppendedID
		}
		// 用一个 1 元素切片让 EventStore.Append 的 seq/id 分配回写到 buf[0]。
		buf := []domain.Event{e}
		if err := r.EventStore.Append(buf); err != nil {
			return fmt.Errorf("append event: %w", err)
		}
		if err := r.State.Apply(buf); err != nil {
			return fmt.Errorf("apply event: %w", err)
		}
		lastAppendedID = buf[0].ID
		appended = append_(appended, buf[0])
		return nil
	}

	// TurnStarted 由调用方在 Step 之前追加,以保证 turnID 已经落库;
	// 若未追加则拒绝执行,避免协议错位。
	view, err := r.State.View(sessionID)
	if err != nil {
		return nil, fmt.Errorf("state.View: %w", err)
	}
	turn, ok := view.LastTurn[taskID]
	if !ok || turn.ID != turnID {
		return nil, errors.New("turn not started (append TurnStarted before Step)")
	}

	// ---- Fold + Project: 把当前 SessionView 投影成 Context ----
	c, err := r.Context.Assemble(ctx, sessionID, taskID)
	if err != nil {
		return appended, fmt.Errorf("context.Assemble: %w", err)
	}
	c.TurnID = turnID

	// ---- Compile: Context → Messages ----
	msgs, err := r.Prompt.Compile(c)
	if err != nil {
		return appended, fmt.Errorf("prompt.Compile: %w", err)
	}

	// ---- Chat: 请求 LLM ----
	if err := append(domain.Event{
		Type: domain.EvtLLMRequested,
		Payload: domain.PayloadLLMRequested{
			Model:    "reference",
			Messages: msgs,
			Tools:    c.Tools,
		},
	}); err != nil {
		return appended, err
	}
	resp, err := r.LLM.Chat(ctx, msgs, c.Tools)
	if err != nil {
		return appended, fmt.Errorf("llm.Chat: %w", err)
	}
	if err := append(domain.Event{
		Type: domain.EvtLLMReplied,
		Payload: domain.PayloadLLMReplied{
			Assistant: resp.Assistant,
			ToolCalls: resp.ToolCalls,
			TokensIn:  resp.TokensIn,
			TokensOut: resp.TokensOut,
		},
	}); err != nil {
		return appended, err
	}

	// ---- Emit: Executor 驱动工具调用,把 tool_call/tool_return 变成 Event ----
	if len(resp.ToolCalls) > 0 {
		toolTurn := domain.Turn{ID: turnID, TaskID: taskID}
		toolEvents, err := r.Executor.Run(ctx, toolTurn)
		if err != nil {
			return appended, fmt.Errorf("executor.Run: %w", err)
		}
		for _, e := range toolEvents {
			if err := append(e); err != nil {
				return appended, err
			}
		}
	}

	// ---- TurnEnded ----
	if err := append(domain.Event{
		Type: domain.EvtTurnEnded,
		Payload: domain.PayloadTurnEnded{
			Status:    domain.TurnDone,
			TokensIn:  resp.TokensIn,
			TokensOut: resp.TokensOut,
		},
	}); err != nil {
		return appended, err
	}
	return appended, nil
}

// append_ 避免与匿名闭包 append 同名。
func append_(dst []domain.Event, e domain.Event) []domain.Event {
	return append(dst, e)
}
