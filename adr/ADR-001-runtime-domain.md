# ADR-001: Runtime 的边界与职责

- **状态**: Accepted
- **日期**: 2026-07-09
- **决策者**: —

## Context

"Agent Runtime" 一词在业界含义不统一：有人指 LLM 推理引擎，有人指 Agent 框架，有人指编排层。缺乏边界定义会导致后续章节内容混乱、代码模块职责重叠。

本项目需要一个明确的 Runtime 边界，作为全书与参考实现的锚点。

## Decision

Agent Runtime 定义为：**接收任务、驱动一个或多个 Agent 完成任务并返回结果的运行时系统**。其职责范围包含：

1. **会话与任务生命周期** —— Session、Task、Turn 的创建、调度、终止。
2. **上下文管理** —— 组装、裁剪、压缩发送给 LLM 的上下文。
3. **状态机** —— Agent 内部状态与持久化。
4. **执行调度** —— 工具调用、并行、超时、重试、取消。
5. **记忆与知识接入** —— 与短期/长期记忆、RAG 的对接协议。
6. **可观测性与治理** —— Trace、成本、权限、审计。

**不在 Runtime 边界内**：

- LLM 推理引擎本身（视为外部服务）。
- 具体业务工具的实现（Runtime 只提供注册与调用协议）。
- 前端 UI 与产品逻辑。

## Consequences

- 各章节按上述职责切分,避免交叉。
- `runtime-go/` 目录按职责划分模块。
- 具体模型、具体工具、具体 UI 都可插拔。

## Alternatives

- **把 Runtime 定义为整个 Agent 应用** —— 边界过宽,书会失焦。
- **把 Runtime 定义为纯编排层** —— 边界过窄,忽略状态、记忆、可观测性等横切能力。

## References

- [ch01 Runtime Domain Model](../chapters/ch01-runtime-domain.md)
- [ADR-003 Runtime 与 DDD 对应关系](ADR-003-ddd-mapping.md)
