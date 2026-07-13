# STYLE GUIDE

## 语言

- 中文正文，专有名词保留英文（如 Runtime、Context、Tool Call）。
- **部分 / 章节标题用中文**（如 `第 4 章 · 上下文引擎`），不要用纯英文标题；专有名词可嵌在中文标题里（如 `Prompt 编译器`）。
- 代码、命令、路径使用等宽字体。
- 避免营销语（"强大"、"极致"、"业界领先"）。
- 标点：正文中逗号、冒号统一用半角（`,` `:`）；句号、顿号、引号保留全角（`。` `、`）。全书章节以此为准。

## 章节结构

每章建议包含：

1. **问题** — 该章解决什么痛点。
2. **概念** — 抽象与术语。
3. **设计** — 结构、接口、时序。
4. **实现** — 关键代码，引用 `runtime-go/` 与 `runtime-rs/` 中的实现。
5. **取舍** — 替代方案与选择理由。
6. **参考** — 论文、开源实现、相关 ADR。

## 图示

- 架构图放 `diagrams/`，同时提交源文件（`.mmd` / `.drawio`）与导出的 `.svg`。
- 时序图优先 Mermaid。

## 代码

- 参考实现放 `runtime-go/<module>/` 与 `runtime-rs/src/<module>/`，两语言字段逐一对齐。一处代码只在一章讲解。
- 章节内贴出的代码片段来自参考实现，附相对路径链接；同一概念先给 **Go**、再给 **Rust**（用粗体子标题分隔）。

## ADR

- 编号从 `ADR-000` 起，一次一变更。
- 状态：`Proposed` / `Accepted` / `Superseded by ADR-XXX`。
- 结构：Context → Decision → Consequences → Alternatives。

## 提交

- 提交信息使用 `type(scope): message`，type 为 `book` / `adr` / `runtime` / `diagram` / `chore`。
