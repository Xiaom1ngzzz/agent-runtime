# Agent Runtime

Building an Agent Runtime from First Principles —— 一本关于 Agent 运行时设计与实现的书,配套 Go / Rust 两份参考实现。

在线阅读: <https://xiaom1ngzzz.github.io/agent-runtime/>

## Structure

- `BOOK.md` — 目标与写作原则。
- `STYLE_GUIDE.md` — 写作约定。
- `ROADMAP.md` — 全书大纲与进度。
- `chapters/` — 章节正文。
- `adr/` — Architecture Decision Records。
- `runtime-go/` — Go 参考实现(`domain/`、`context/`、`state/` …)。`go build ./...`。
- `runtime-rs/` — Rust 参考实现,与 Go 逐字段对齐。`cargo build`。
- `diagrams/` — 架构与时序图源文件。
- `docs/` — MkDocs 站点入口(符号链接,不修改)。

## How to read

从 `BOOK.md` 开始,然后 `ROADMAP.md`,再按章节顺序读。

## Local preview of the site

```bash
pip install --user mkdocs-material
mkdocs serve
```

浏览器访问 <http://127.0.0.1:8000>。
