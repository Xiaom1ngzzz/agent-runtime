# ADR-007: 工具副作用协议

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-002 · Runtime 数据流协议](ADR-002-dataflow-protocol.md), [ADR-006 · Command/Event 幂等](ADR-006-command-event-idempotency.md)
- **落地章节**: [ch08 · 执行器](../chapters/ch08-executor.md), [ch11 · 生产边界](../chapters/ch11-production-boundaries.md)

## Context

ADR-002 明确 Emit 不保证原子性:副作用可能已发生,Event 尚未落库。ch08 Round 2 的一次性 `Run` 适合幂等工具,不适合支付、发邮件等 mutating 操作。

## Decision

1. **意图先行**:mutating 工具须先追加 `ToolCalled`(或 `ToolIntentRecorded`),再执行;执行结果以 `ToolReturned` 闭合。崩溃恢复时以 `CallID` 判断"已意图未结果"状态。
2. **分类**:注册表声明 `read_only | idempotent_mutating | non_idempotent_mutating`;Executor 对第三类在重复 `CallID` 时拒绝重跑,转人工或 query 状态。
3. **Outbox**:可选模式——`ToolCalled` 触发 outbox worker 执行,worker 成功后再 `ToolReturned`;与 ADR-006 dedup 共用键。
4. **输出边界**:tool result 大小/深度上限(ch08 §8.3.1),防止撑爆 Context。

## Consequences

### 正向

- 审计链完整:意图 → 执行 → 结果均可追溯;
- 与 ch01 "中断后副作用可解释"对齐。

### 负向

- 非幂等工具恢复复杂,可能需要 HITL;
- submit/resume(ch08 预留)落地前,长工具仍用超时失败策略。

## Alternatives

**A. Saga 补偿自动回滚** —— ch07 Round 2 未实现;业务成本高。

**B. 所有工具当 read-only** —— 不现实。

## References

- ch08 §8.3, ch09 Recover
- Hector Garcia-Molina et al., *Sagas* (1987)
