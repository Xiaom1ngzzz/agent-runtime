# 第 11 章 · 生产边界

> ch01–ch10 把 Runtime 从概念走到可跑、可恢复、可冒烟评测。这一章收束**上线前必须显式决策的横切边界**——安全、幂等、故障注入、Provider 矩阵——多数在参考实现中标注为"扩展",但生产不能省略。

---

## 11.1 问题:能跑 ≠ 能上线

参考实现刻意保持教学清晰:两把锁、一次性 Executor、内存 Store、无租户隔离。以下四类缺口在生产上会同时暴露:

1. **并发**:多实例写同一 Session,seq 竞争或双 Turn。
2. **幂等**:网关重试、崩溃恢复、Planner 批追加导致重复副作用。
3. **安全**:Memory 跨 Session 检索漏 tenant;tool 参数未校验;审计被误当成授权。
4. **可运维**:无保留/删除策略;无 Provider 能力矩阵;故障注入未覆盖租约丢失。

本章不重复实现细节,给出**协议清单与 ADR 索引**,供团队按部署级别勾选。

---

## 11.2 Session 并发与租约

**契约摘要**([ADR-005](../adr/ADR-005-session-concurrency-lease.md)):

- 每个 `session_id` 写路径同一时刻一个租约持有者;
- 租约丢失 → Turn 失败 `LeaseLost`,不猜测性写;
- 读路径 `as_of_seq ≤ view.MaxSeq`(与 ADR-002 Project 一致)。

**故障注入建议**:模拟续租失败、抢占租约、持有者僵死——断言无 seq 回退、无重复 TurnStarted。

---

## 11.3 Command / Event 幂等

**契约摘要**([ADR-006](../adr/ADR-006-command-event-idempotency.md)):

- 外部 Command 带 `idempotency_key`;
- `ToolCalled.CallID` 去重,已有 `ToolReturned` 则不再 dispatch;
- Planner 规划 Event 单次原子 `Append` 或确定性 id 幂等 spawn。

**与 Fold C2 的区分**:C2 是"同 Event 流 → 同 View"的**确定性**;幂等是"同 Command 不重复副作用"。

**故障注入**:崩溃在 `ToolCalled` 后 / `ToolReturned` 前;重复 POST 同一 idempotency_key——断言副作用至多一次(对声明为幂等的工具)。

---

## 11.4 工具副作用协议

**契约摘要**([ADR-007](../adr/ADR-007-tool-side-effect-protocol.md)):

| 级别 | 恢复策略 |
|------|----------|
| `read_only` | 可安全重跑 |
| `idempotent_mutating` | 以 CallID 去重 |
| `non_idempotent_mutating` | 禁止盲重跑;query 状态或 HITL |

意图先行 → 执行 → `ToolReturned`;可选 outbox。输出大小/深度上限见 ch08 §8.3.1。

---

## 11.5 Runtime 安全边界

**契约摘要**([ADR-008](../adr/ADR-008-runtime-security-boundaries.md)):

- `Principal` 与 Session 写路径绑定;
- Memory **`TenantID` 强制过滤**(ch05 §5.3);
- Tool JSON Schema 入参校验 + 出参截断;
- Runtime 记录因果,**不替代** IAM/审批。

---

## 11.6 数据保留与删除

**契约摘要**([ADR-009](../adr/ADR-009-data-retention-deletion.md)):

- EventStore:retention topic / 时间策略,非 compacted 丢历史;
- Cold → Archive + `ContextArchived` 索引 Event;
- 用户删除:tombstone Event,Memory 擦除,保留 seq 空洞;
- Fold 跳过 tombstone 区间。

---

## 11.7 Provider 能力矩阵(概要)

生产 Prompt Compiler 选型时,至少核对:

| 能力 | OpenAI | Anthropic | 本书基线 |
|------|--------|-----------|----------|
| Tool schema 严格校验 | Structured Outputs / `strict` | tool `input_schema` | 强制 |
| JSON Mode | 支持 | 支持 | 仅无 schema 的简单结构 |
| 连续 user | 接受 | **合并** | Compiler 侧合并(ch06) |
| Tool result 形态 | 多条 `role=tool` | **单条 user 聚合** | Adapter 负责 |
| Prompt Cache | 前缀自动 | `cache_control` 显式 | 前缀内容哈希,非对象 ID |

完整差异测试见 ch06 `ch06_provider_diff_test`。

---

## 11.8 Eval 与生产门禁

ch10 Round 2 比较器覆盖**协议不变量**,不覆盖 LLM 质量/成本统计门禁。生产建议分层:

| 层级 | 门禁 | 工具 |
|------|------|------|
| L0 协议 | 结构、终态、CallID | `CompareStreams` |
| L1 恢复 | Checkpoint 后 View | `ScoreView` |
| L2 质量 | 任务成功率、措辞 | 金标 + 多次采样 / judge |
| L3 成本延迟 | p95 token/延迟 | trace 聚合 + 预算 |

---

## 11.9 取舍记录

| 决策 | 选择 | 代价 |
|------|------|------|
| 租约 | 显式分布式租约 | 基础设施依赖 |
| 幂等 | CallID + idempotency_key | dedup 存储 |
| 安全 | 薄 Runtime + 上层 IAM | 集成工作 |
| 删除 | Tombstone,不硬删 seq | Fold 复杂度 |

---

## 11.10 小结

- 生产边界由 ADR-005–009 收束:并发、幂等、工具副作用、安全、保留删除。
- 参考实现故意不全部落地;上线 checklist 应逐条对照本章与 ch11 引用的 ADR。
- Provider 矩阵与 Eval 分层避免"冒烟绿灯 = 生产就绪"的错觉。

---

## 参考

- [ADR-005 · Session 并发与租约](../adr/ADR-005-session-concurrency-lease.md)
- [ADR-006 · Command/Event 幂等](../adr/ADR-006-command-event-idempotency.md)
- [ADR-007 · 工具副作用协议](../adr/ADR-007-tool-side-effect-protocol.md)
- [ADR-008 · Runtime 安全边界](../adr/ADR-008-runtime-security-boundaries.md)
- [ADR-009 · 数据保留与删除](../adr/ADR-009-data-retention-deletion.md)
- 相关:`ch05-memory.md`、`ch08-executor.md`、`ch10-eval.md`
