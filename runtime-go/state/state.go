// Package state 定义状态与事件存储接口。
// State 是 Event 流的折叠结果的只读视图；EventStore 提供追加与加载。
// 实现见 ch03-state-event.md 与 ch09-checkpoint.md。
package state

import "agent-runtime-go/domain"

type State interface {
	Apply(events []domain.Event) error
	View(sessionID string) (domain.SessionView, error)
}

type EventStore interface {
	Append(events []domain.Event) error
	Load(sessionID string) ([]domain.Event, error)
	// LoadFrom 返回 seq > fromSeq 的所有事件，按 seq 升序。
	// fromSeq=0 等价于 Load(sessionID)。用于 §3.6 Snapshot 恢复。
	LoadFrom(sessionID string, fromSeq int64) ([]domain.Event, error)
}
