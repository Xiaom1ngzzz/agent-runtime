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
- [ ] `chapters/ch06-prompt-compiler.md` —— Prompt Compiler
- [ ] `runtime-go/context/` & `runtime-rs/src/context.rs` —— 上下文引擎参考实现

## Part III — Execution（执行系统）

- [ ] `chapters/ch07-planner.md` —— Planner & Task Graph
- [ ] `chapters/ch08-executor.md` —— Executor
- [ ] `chapters/ch09-checkpoint.md` —— Checkpoint & Recovery
- [ ] `runtime-go/state/` & `runtime-rs/src/state.rs` —— 状态 / 事件 / Checkpoint 参考实现

## Part IV — Evolution（演进）

- [ ] `chapters/ch10-eval.md` —— Evaluation & Optimization

## 里程碑

- **M1 Foundation**：Part I 完成 + ADR-001 落地。
- **M2 Context**：Part II 完成，`runtime-go/context/` 与 `runtime-rs/src/context.rs` 均可运行 demo。
- **M3 Execution**：Part III 完成，端到端跑通一个最小 Agent。
- **M4 Evolution**：Part IV 完成，配套评测框架。
