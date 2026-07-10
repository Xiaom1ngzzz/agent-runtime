// Package memory —— MemoryStore 接口。见 ch05 §5.4。
//
// EventStore 是"发生过什么的账本"(必须精确),Memory 是"可能相关的参考"(允许模糊)。
// 两个组件生命周期正交:EventStore 与 Session 同生命周期,Memory 跨 Session。
package memory

import (
	stdctx "context"

	"agent-runtime-go/domain"
)

// MemoryStore 是 Memory 层的抽象接口。见 ch05 §5.4.1。
//
// 契约:
//   - Upsert 幂等(同 Key/Version 写两次结果一致)
//   - Query 是纯读,但仍然是 IO(不能在 Assemble 里调)
//   - 不承诺一致性:Upsert 后立即 Query 可能读不到
//   - 顺序:Query 结果按 score 降序
type MemoryStore interface {
	// Upsert 插入或更新。若 Key 已存在:比较 Version,新的 > 旧的才生效。
	Upsert(ctx stdctx.Context, item domain.MemoryItem) error

	// Query 按查询召回 Top-K。见 §5.5。
	Query(ctx stdctx.Context, q domain.Query) ([]domain.MemoryRef, error)

	// Expire 让指定 Key 立即过期(相当于 ExpiresAt = now)。
	Expire(ctx stdctx.Context, key string) error
}
