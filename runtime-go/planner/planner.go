// Package planner 定义 Planner 接口与 Task Graph 协调(ch07)。
//
// Planner 负责:根据当前 SessionView,为某个父 Task 决定是否派生子 Task,
// 以及何时发出 ProgressUpdated。它不调用 LLM、不执行工具——那些是 Runtime.Step / Executor。
package planner

import (
	stdcontext "context"

	"agent-runtime-go/domain"
)

// Planner 根据视图产出规划侧 Event(SubTaskSpawned / TaskCreated / ProgressUpdated)。
// 返回的 Event 由调用方(上层 Loop)Append+Apply;Planner 本身不写 EventStore。
type Planner interface {
	Plan(ctx stdcontext.Context, view domain.SessionView, taskID string) ([]domain.Event, error)
}

// ProgressTrigger 决定何时从 Event 流折叠出新的 Progress。
type ProgressTrigger interface {
	ShouldUpdate(view domain.SessionView, taskID string, recent []domain.Event) bool
}
