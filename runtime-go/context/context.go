// Package context 定义上下文引擎接口。
// 上下文引擎负责:把 Fold 后的 SessionView 投影成一次 Turn 需要的 Context。
// 实现可从 EventStore 只读展开消息原文(见 ADR-002、ch03 §3.5.1、ch04 §4.4)。
package context

import (
	stdcontext "context"

	"agent-runtime-go/domain"
)

// ContextEngine 组装一次 Turn 的上下文。
// 输入：会话与任务标识；输出：可直接交给 PromptCompiler 的 domain.Context。
type ContextEngine interface {
	Assemble(ctx stdcontext.Context, sessionID, taskID string) (domain.Context, error)
}
