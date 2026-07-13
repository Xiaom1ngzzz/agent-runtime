# ADR-005: Session 并发与租约

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-002 · Runtime 数据流协议](ADR-002-dataflow-protocol.md), [ADR-003 · DDD 对应关系](ADR-003-ddd-mapping.md)
- **落地章节**: [ch11 · 生产边界](../chapters/ch11-production-boundaries.md)

## Context

ch01 §1.1 的"并发"痛点与 ch03 §3.4.2 的"同 session 串行"契约,在单进程 demo 里靠一把锁够用。生产上同一 Session 可能同时收到:

- 用户新消息;
- 定时 Progress 刷新;
- Compressor 异步摘要;
- 多实例 Runtime 水平扩展。

若无显式租约,会出现双写 Event、seq 竞争、Turn 状态不一致。

## Decision

1. **写路径**:每个 `session_id` 同一时刻只允许一个**租约持有者**执行 `Append + Apply`(可映射为分布式锁 / DB advisory lock / 单 leader 分区)。
2. **租约字段**:`{holder, expires_at, epoch}`;续租由活跃 Turn 的协调器负责;过期后另一实例可抢占,但须先 `Recover` 到一致 View 再写。
3. **读路径**:`State.View` 与 Project 的 EventStore 只读展开可在租约外执行,但 **`as_of_seq` 不得超过持有者已提交的最大 seq**。
4. **冲突策略**:租约丢失 → 当前 Turn 失败返回 `LeaseLost`,不猜测性追加 Event。

## Consequences

### 正向

- 多实例部署时 Session 写路径可推理;
- 与 ch03 seq 单调、ADR-002 Append+Apply 成对约束闭合。

### 负向

- 需要租约存储(Redis / DB)与续租心跳;
- 长 Turn 须定期续租,否则慢工具可能丢租约。

## Alternatives

**A. 乐观并发(seq 冲突时重试)** —— 适合低冲突;高并发 Session 重试风暴。

**B. 全局单写 leader** —— 简单但扩展性差。

**C. CRDT 合并 Event** —— 过重;Event Sourcing 语义要求全序。

## References

- ch03 §3.4.2, ch09 Recover
- Leslie Lamport, *Time, Clocks, and the Ordering of Events* (1978)
