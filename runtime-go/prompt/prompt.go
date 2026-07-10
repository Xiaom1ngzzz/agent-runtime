// Package prompt 定义 Prompt 编译器接口。
// Prompt 编译器负责：把结构化的 domain.Context 转成 LLM Provider 能吃的 Messages。
// 实现见后续章节（ch06-prompt-compiler.md）。
package prompt

import "agent-runtime-go/domain"

// Messages 是准备发送给 LLM 的最终消息序列。
type Messages []domain.Message

type PromptCompiler interface {
	Compile(c domain.Context) (Messages, error)
}
