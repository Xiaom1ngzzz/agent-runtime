// Package executor 定义执行器接口。
// 执行器负责：驱动一个 Turn 完成——调 LLM、分发工具、回收结果、生成 Event 流。
// 实现见 ch08-executor.md。
package executor

import (
	stdcontext "context"

	"agent-runtime-go/domain"
)

type Executor interface {
	Run(ctx stdcontext.Context, turn domain.Turn) ([]domain.Event, error)
}
