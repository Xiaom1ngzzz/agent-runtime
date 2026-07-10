// Package llm 定义 LLM Provider 接口。
// LLM Provider 是 Runtime 之外的相邻系统；本包只声明协议，不含具体实现。
package llm

import (
	stdcontext "context"

	"agent-runtime-go/domain"
	"agent-runtime-go/prompt"
)

type LLMProvider interface {
	Chat(ctx stdcontext.Context, msgs prompt.Messages, tools []domain.Tool) (domain.LLMResponse, error)
}
