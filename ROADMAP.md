# ROADMAP

> 主线以 `README.rst` 中的目录为准，本文件为工程落地视图（章节文件 + 参考实现 + ADR 的对应关系）。

## Part I — Foundation（世界观）

- [x] `chapters/ch01-runtime-domain.md` —— Runtime Domain Model
- [x] `chapters/ch02-runtime-dataflow.md` —— Runtime Data Flow
- [x] `chapters/ch03-state-event.md` —— State & Event Model
- [x] `adr/ADR-001-runtime-domain.md` —— Runtime 的边界与职责
- [x] `adr/ADR-002-dataflow-protocol.md` —— Runtime 数据流协议
- [x] `adr/ADR-003-ddd-mapping.md` —— Runtime 与 DDD 对应关系

## Part II — Context System（上下文系统）

- [x] `chapters/ch04-context-engine.md` —— Context Engine（文字，代码 Round 2 落地）
- [x] `chapters/ch05-memory.md` —— Memory Architecture（文字，代码 Round 2 落地）
- [x] `chapters/ch06-prompt-compiler.md` —— Prompt Compiler（文字，代码 Round 2 落地）
- [x] `runtime-go/{context,compressor,memory,prompt}/` & `runtime-rs/src/{context,compressor,memory,prompt}/` —— ch04–ch06 Round 2 参考实现与测试

## Part III — Execution（执行系统）

- [x] `chapters/ch07-planner.md` —— Planner & Task Graph
- [x] `chapters/ch08-executor.md` —— Executor
- [x] `chapters/ch09-checkpoint.md` —— Checkpoint & Recovery
- [x] `adr/ADR-004-task-graph-saga.md` —— Task Graph 与 Saga
- [x] `runtime-go/{planner,executor,state}/` & `runtime-rs/src/{planner,executor,state}/` —— 规划 / 执行 / Checkpoint 参考实现
- [x] `runtime-go/examples/m3/` —— M3 最小 Agent 端到端

## Part IV — Evolution（演进）

- [x] `chapters/ch10-eval.md` —— Evaluation & Optimization
- [x] `runtime-go/eval/` & `runtime-rs/src/eval/` —— 最小评测框架

## 里程碑

- [x] **M1 Foundation**：Part I 三章 + ADR-001/002/003 落地。
- [x] **M2 Context**：Part II 完成，Go/Rust 的 Compression Cycle、Memory Cycle、Provider Diff 测试可运行。（已收口：ch04–ch06 章节文字 + Round 2 最小闭环；生产扩展项在各章“实现状态”中标注）
- [x] **M3 Execution**：Part III 完成，端到端跑通一个最小 Agent（Planner → Executor → Checkpoint → Eval）。
- [x] **M4 Evolution**：Part IV 完成，配套评测框架（`CompareStreams` / Score）。
