package prompt

import "agent-runtime-go/domain"

// ReferenceCompiler —— 厂商无关的普通格式(与 memfakes 里 pass-through 一致)。
// 用于本地测试和跨 Provider 对比。见 ch06 §6.6。
type ReferenceCompiler struct{}

func (ReferenceCompiler) Compile(c domain.Context) (Messages, error) {
	if err := checkMessages(c.Messages); err != nil {
		return nil, err
	}
	// pass-through
	return Messages(c.Messages), nil
}
