# ADR-009: 数据保留与删除

- **状态**: Accepted
- **日期**: 2026-07-13
- **决策者**: —
- **上游**: [ADR-001 · Runtime 边界](ADR-001-runtime-domain.md), [ADR-003 · DDD 对应](ADR-003-ddd-mapping.md)
- **落地章节**: [ch04 · 上下文引擎](../chapters/ch04-context-engine.md) §4.3, [ch11 · 生产边界](../chapters/ch11-production-boundaries.md)

## Context

ch04 定义 Context 生命周期 Hot → Warm → Cold → Archive。EventStore append-only 与 GDPR/合规删除要求冲突;Memory 允许 upsert/过期但 Event 原文仍在。

## Decision

1. **分级保留**:
   - **Hot/Warm**:EventStore 全量,按 Session 保留策略(如 90 天活跃)。
   - **Cold**:原文可迁 Archive(S3/Parquet),EventStore 留索引 Event(`ContextArchived`)。
   - **Archive**:合规长期存储,独立访问控制。
2. **删除请求**:用户删除权 → 对 Session 打 `SessionDeletionRequested` Event,异步作业: tombstone Event、擦除 Memory 条目、Archive 对象标记删除。**不物理删除历史 Event id**(保留空洞 seq),Fold 跳过 tombstone 区间。
3. **Memory 过期**:`ExpiresAt` 到期后 Query 不返回;后台压缩索引。
4. **Kafka/EventStoreDB**:使用 retention policy 而非 compacted topic 承载完整事件流(ch03 §3.4.3)。

## Consequences

### 正向

- 合规路径清晰;与 ch04 Archive 层闭合。

### 负向

- Tombstone + 空洞 seq 增加 Fold 复杂度;
- 物理擦除 Archive 需额外作业与证明。

## Alternatives

**A. 硬删除 Event 行** —— 破坏 seq 连续性与审计。

**B. 永不删除** —— 不合规。

## References

- ch03 §3.4.3 L4, ch04 §4.3 生命周期表
- GDPR Art. 17 (Erasure)
