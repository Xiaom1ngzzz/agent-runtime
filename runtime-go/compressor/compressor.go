// Package compressor —— 独立于 Step 的 GC。见 ch04 §4.5。
//
// Compressor 不在 ch02 §2.4 的 5 段协议里 —— 它像 GC,独立、可选、有副作用。
// 由上层 Loop 触发,产出 ContextCompressed Event 追加到 EventStore。
// Assemble 只读事实,不参与摘要生成。
package compressor

import (
	stdctx "context"
	"errors"
	"fmt"

	"agent-runtime-go/domain"
	"agent-runtime-go/state"
)

// Compressor 是 §4.5.1 定义的接口。
//
// Tick 检查当前 Session 需不需要压缩;需要就产出 ContextCompressed Event。
// 返回 nil 表示"不需要压缩";返回 []Event 表示"追加这些"。
type Compressor interface {
	Tick(ctx stdctx.Context, sessionID string) ([]domain.Event, error)
}

// Summarizer 抽象"如何生成 Summary"这件事。
// 生产实现里是 LLM 调用;测试里是 fake/剧本化。
// Summarizer 是唯一允许 IO 的组件(§4.4.2 反例的正确出口)。
type Summarizer interface {
	Summarize(ctx stdctx.Context, sessionID, taskID string, events []domain.Event) (domain.Summary, error)
}

// ByTurns 是"按 WorkingSet 长度触发"的最简 Compressor。§4.5.2。
//
// 策略:未 Superseded 的 TurnDigest 数量 >= Threshold 时,把它们摘要成一条 Summary。
type ByTurns struct {
	State      state.State
	Store      state.EventStore
	Summarizer Summarizer
	Threshold  int // 触发阈值,默认 3
}

// Tick 检查阈值 → 找出未 Superseded 段 → 拉原文 → 调 Summarizer → 追加 Event。
func (c *ByTurns) Tick(ctx stdctx.Context, sessionID string) ([]domain.Event, error) {
	if c.Threshold <= 0 {
		c.Threshold = 3
	}
	view, err := c.State.View(sessionID)
	if err != nil {
		return nil, fmt.Errorf("state.View: %w", err)
	}

	// 找出"最老的一批未 Superseded 且属于同一 Task 的 TurnDigest"。
	var target []domain.TurnDigest
	var taskID string
	for _, d := range view.WorkingSet {
		if d.Superseded {
			continue
		}
		if taskID == "" {
			taskID = d.TaskID
		}
		if d.TaskID != taskID {
			// 一次只压缩同一 Task,避免混语义。
			break
		}
		target = append(target, d)
	}
	if len(target) < c.Threshold {
		return nil, nil
	}
	// 只压缩"最老的 half",保留最近的原文(§4.6 "保护最近对话"这段)。
	toCompress := target[:len(target)/2]
	if len(toCompress) == 0 {
		return nil, nil
	}

	// 收集覆盖 seq 范围内的原始 Event。
	fromSeq := toCompress[0].FromSeq
	toSeq := toCompress[len(toCompress)-1].ToSeq
	allEvents, err := c.Store.LoadFrom(sessionID, fromSeq-1)
	if err != nil {
		return nil, fmt.Errorf("store.LoadFrom: %w", err)
	}
	var scoped []domain.Event
	for _, e := range allEvents {
		if e.Seq >= fromSeq && e.Seq <= toSeq {
			scoped = append(scoped, e)
		}
	}

	// 调 Summarizer(唯一允许 IO 的地方)。
	if c.Summarizer == nil {
		return nil, errors.New("compressor: no Summarizer configured")
	}
	summary, err := c.Summarizer.Summarize(ctx, sessionID, taskID, scoped)
	if err != nil {
		// 降级:追加 CompressionSkipped Event(§4.9),交给上层 Loop 决定要不要重试。
		return []domain.Event{{
			SessionID: sessionID, TaskID: taskID,
			Type: domain.EvtCompressionSkipped,
			Payload: domain.PayloadCompressionSkipped{
				Reason: "summarizer_error", Detail: err.Error(),
			},
		}}, nil
	}
	summary.SessionID = sessionID
	summary.TaskID = taskID
	summary.FromSeq = fromSeq
	summary.ToSeq = toSeq

	return []domain.Event{{
		SessionID: sessionID, TaskID: taskID,
		Type: domain.EvtContextCompressed,
		Payload: domain.PayloadContextCompressed{
			FromSeq: fromSeq, ToSeq: toSeq,
			Strategy: fmt.Sprintf("turns:%d", c.Threshold),
			Summary:  summary,
		},
	}}, nil
}

// ---------- 一个 Summarizer 的最简 fake,用于测试 ----------

// ScriptedSummarizer 是"按预设剧本返回 Summary"的假实现。
// 生产环境里替换成 LLM-backed Summarizer(会在 ch06 讨论)。
type ScriptedSummarizer struct {
	Script []domain.Summary
	idx    int
}

func NewScriptedSummarizer(script []domain.Summary) *ScriptedSummarizer {
	return &ScriptedSummarizer{Script: script}
}

func (s *ScriptedSummarizer) Summarize(_ stdctx.Context, _, _ string, _ []domain.Event) (domain.Summary, error) {
	if s.idx >= len(s.Script) {
		return domain.Summary{}, errors.New("scripted summarizer exhausted")
	}
	out := s.Script[s.idx]
	s.idx++
	return out, nil
}
