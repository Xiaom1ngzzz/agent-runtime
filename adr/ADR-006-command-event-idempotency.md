# ADR-006: Command / Event 幂等

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-002 · Runtime 数据流协议](ADR-002-dataflow-protocol.md)
- **落地章节**: [ch11 · 生产边界](../chapters/ch11-production-boundaries.md), ch07 §7.3.2, ch08 §8.5

## Context

ch02 §2.7 与 ch08 指出:工具副作用成功后、`ToolReturned` 落库前进程崩溃,恢复时可能重复执行。ch07 Planner 一次返回多条规划 Event,与 EventStore 原子批要求并存。上层 Loop 重试、网关重复投递也会制造重复 Command。

## Decision

1. **Command 幂等键**:每个外部触发的写操作携带 `idempotency_key`(客户端生成或网关注入);协调器在 `Append` 前查 dedup 表,已处理则返回上次结果。
2. **Tool 幂等**:`ToolCalled.CallID` 作为副作用去重键;Executor 在 dispatch 前检查 EventStore 是否已有同 `call_id` 的 `ToolReturned`。
3. **Event 幂等**:同 `idempotency_key` 的重复 `Append` 必须 no-op 或返回已分配 seq,不生成第二条事实。
4. **Planner 批**:规划 Event 要么单次原子 `Append`,要么每条带确定性 id 供 Fold 跳过重复 spawn。

**区分**:Fold 的 C2 是**确定性**(同流同 View),不是 Command 层的**幂等**——本 ADR 管后者。

## Consequences

### 正向

- 收窄 ch08 "副作用成功、结果未落库"窗口;
- 网关/Loop 重试可安全。

### 负向

- 需要 dedup 存储与 TTL;
- 非幂等工具须业务声明,不能默认安全重试。

## Alternatives

**A. 仅依赖 exactly-once 消息队列** —— 工具副作用仍在 Runtime 外,不够。

**B. 两阶段提交** —— 过重;与 Event Sourcing append-only 冲突。

## References

- ch08 §8.5, ch09 §9.1
- ADR-007 工具副作用协议
