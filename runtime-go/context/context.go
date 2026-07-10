// Package context 定义上下文引擎接口。
// 上下文引擎负责：把 State 中相关的 Event 流投影成一次 Turn 需要的消息序列。
// 实现见后续章节（ch04-context-engine.md）。
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
