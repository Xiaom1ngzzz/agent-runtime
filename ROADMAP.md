# ROADMAP

> 主线以 `README.rst` 中的目录为准，本文件为工程落地视图（章节文件 + 参考实现 + ADR 的对应关系）。

## 第一部分 — 基础（世界观）

- [x] `chapters/ch01-runtime-domain.md` —— 运行时领域模型
- [x] `chapters/ch02-runtime-dataflow.md` —— 运行时数据流
- [x] `chapters/ch03-state-event.md` —— 状态与事件模型
- [x] `adr/ADR-001-runtime-domain.md` —— Runtime 的边界与职责
- [x] `adr/ADR-002-dataflow-protocol.md` —— Runtime 数据流协议
- [x] `adr/ADR-003-ddd-mapping.md` —— Runtime 与 DDD 对应关系

## 第二部分 — 上下文系统

- [x] `chapters/ch04-context-engine.md` —— 上下文引擎（文字，代码 Round 2 落地）
- [x] `chapters/ch05-memory.md` —— 记忆架构（文字，代码 Round 2 落地）
- [x] `chapters/ch06-prompt-compiler.md` —— Prompt 编译器（文字，代码 Round 2 落地）
- [x] `runtime-go/{context,compressor,memory,prompt}/` & `runtime-rs/src/{context,compressor,memory,prompt}/` —— ch04–ch06 Round 2 参考实现与测试

## 第三部分 — 执行系统

- [x] `chapters/ch07-planner.md` —— 规划器与任务图
- [x] `chapters/ch08-executor.md` —— 执行器
- [x] `chapters/ch09-checkpoint.md` —— 检查点与恢复
- [x] `adr/ADR-004-task-graph-saga.md` —— 任务图与 Saga
- [x] `runtime-go/{planner,executor,state}/` & `runtime-rs/src/{planner,executor,state}/` —— 规划 / 执行 / Checkpoint 参考实现（Rust Executor 当前为同步顺序基线）
- [x] `runtime-go/examples/m3/` —— M3 最小 Agent 端到端

## 第四部分 — 演进

- [x] `chapters/ch10-eval.md` —— 评测与优化
- [x] `runtime-go/eval/` & `runtime-rs/src/eval/` —— 确定性协议比较与恢复冒烟框架

## 第五部分 — 生产边界

- [x] `chapters/ch11-production-boundaries.md` —— 安全、幂等、故障注入、Provider 矩阵概要
- [x] `adr/ADR-005-session-concurrency-lease.md` —— Session 并发与租约
- [x] `adr/ADR-006-command-event-idempotency.md` —— Command/Event 幂等
- [x] `adr/ADR-007-tool-side-effect-protocol.md` —— 工具副作用协议
- [x] `adr/ADR-008-runtime-security-boundaries.md` —— Runtime 安全边界
- [x] `adr/ADR-009-data-retention-deletion.md` —— 数据保留与删除

## 里程碑

- [x] **M1 基础**：第一部分 三章 + ADR-001/002/003 落地。
- [x] **M2 上下文**：第二部分 完成，Go/Rust 的 Compression Cycle、Memory Cycle、Provider Diff 测试可运行。（已收口：ch04–ch06 章节文字 + Round 2 最小闭环；生产扩展项在各章“实现状态”中标注）
- [x] **M3 执行**：第三部分参考路径完成，Go 端到端跑通最小 Agent（Planner → Executor → Checkpoint → Eval 冒烟）；持久化 submit/resume 与跨进程 Checkpoint wire 留作生产扩展。
- [x] **M4 演进**：第四部分确定性协议比较器完成（`CompareStreams` / Score）；随机质量评测、统计门禁与真实 golden suite 留作生产扩展。
- [x] **M5 生产边界**：第五部分 ch11 + ADR-005–009 文字落地；租约、幂等 dedup、持久化 Checkpoint wire 留作生产扩展。
